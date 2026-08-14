import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useEffect, useState, type ChangeEvent, type FormEvent, type ReactNode } from 'react';
import { slurm, type JobSummary, type Partition } from '../services/slurm';

export const Route = createFileRoute('/jobs')({ component: JobsPage });

function jobStateColor(s: string): string {
  const st = (s || '').toUpperCase();
  if (st === 'RUNNING') return '#10b981';
  if (st === 'PENDING' || st === 'HELD' || st === 'CONFIGURING') return '#f59e0b';
  if (st === 'COMPLETED') return '#64748b';
  if (st === 'CANCELLED' || st === 'FAILED' || st === 'TIMEOUT' || st === 'OUT_OF_MEMORY') return '#f43f5e';
  return '#3b82f6';
}

const emptyForm = {
  name: '',
  partition: 'standard',
  nodes: '1',
  tasks: '1',
  time_limit: '60',
  script: '#!/bin/bash\nsleep 30\n',
};

// 作业状态过滤选项（值对齐 job_state 大写形式）
const FILTERS: { label: string; value: string }[] = [
  { label: '全部', value: 'ALL' },
  { label: 'RUNNING', value: 'RUNNING' },
  { label: 'PENDING', value: 'PENDING' },
  { label: 'COMPLETED', value: 'COMPLETED' },
  { label: 'FAILED', value: 'FAILED' },
];

function JobsPage() {
  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [form, setForm] = useState(emptyForm);
  const [submitting, setSubmitting] = useState(false);
  const [acting, setActing] = useState('');
  const [filter, setFilter] = useState('ALL');
  const [partitions, setPartitions] = useState<Partition[]>([]);

  // 分区列表（一次拉取，供提交表单下拉；standard=E核默认 / performance=P核，见 slurm.conf）
  useEffect(() => {
    slurm
      .getPartitions()
      .then((r) => setPartitions(r.partitions || []))
      .catch(() => {});
  }, []);

  const refresh = useCallback(async () => {
    try {
      const r = await slurm.getJobs();
      setJobs(r.jobs || []);
      setError('');
    } catch (e: any) {
      setError(e?.message || '加载作业失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, [refresh]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form.name.trim() || !form.script.trim()) {
      setError('作业名 与 脚本 必填');
      return;
    }
    setSubmitting(true);
    setError('');
    setInfo('');
    try {
      const r = await slurm.submitJob({
        name: form.name.trim(),
        partition: form.partition.trim() || 'standard',
        nodes: Number(form.nodes) || 1,
        tasks: Number(form.tasks) || 1,
        time_limit: String(form.time_limit || '60'),
        script: form.script,
      });
      setInfo(`已提交：作业 #${r.job_id}`);
      setForm(emptyForm);
      await refresh();
    } catch (err: any) {
      setError(`提交失败：${err?.message || err}`);
    } finally {
      setSubmitting(false);
    }
  };

  const act = async (id: number, kind: 'cancel' | 'hold' | 'requeue') => {
    setActing(id + kind);
    setError('');
    setInfo('');
    try {
      const fn = kind === 'cancel' ? slurm.cancelJob : kind === 'hold' ? slurm.holdJob : slurm.requeueJob;
      const r = await fn(id);
      setInfo(`作业 #${id} ${kind}：${r.message}`);
      await refresh();
    } catch (e: any) {
      setError(`作业 #${id} ${kind} 失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  const field = (k: keyof typeof emptyForm) => (e: ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value });

  // 按状态过滤渲染的作业（filter==='ALL' 或 job_state 大写匹配）
  const visibleJobs = jobs.filter((j) => filter === 'ALL' || (j.job_state || '').toUpperCase() === filter);

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1rem' }}>作业管理</h2>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      <form
        onSubmit={submit}
        style={{ background: 'var(--bg-card,#1b1e28)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 12, padding: '1.25rem', marginBottom: '1.5rem', display: 'grid', gap: '0.75rem', boxShadow: 'var(--shadow-card)', transition: 'box-shadow .3s ease' }}
      >
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(150px,1fr))', gap: '0.75rem' }}>
          <Field label="作业名"><input className="form-control" value={form.name} onChange={field('name')} placeholder="my-job" /></Field>
          <Field label="分区">
            <select
              className="form-control"
              value={form.partition}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setForm({ ...form, partition: e.target.value })}
            >
              {(partitions.length > 0 ? partitions.map((p) => p.name) : ['standard']).map((name) => (
                <option key={name} value={name}>
                  {name}
                  {name === 'performance' ? '（P 核）' : name === 'standard' ? '（E 核）' : ''}
                </option>
              ))}
            </select>
          </Field>
          <Field label="节点数"><input className="form-control" type="number" min="1" value={form.nodes} onChange={field('nodes')} /></Field>
          <Field label="任务数"><input className="form-control" type="number" min="1" value={form.tasks} onChange={field('tasks')} /></Field>
          <Field label="时限(分钟)"><input className="form-control" value={form.time_limit} onChange={field('time_limit')} /></Field>
        </div>
        <Field label="脚本">
          <textarea
            className="form-control"
            rows={4}
            value={form.script}
            onChange={(e) => setForm({ ...form, script: e.target.value })}
            style={{ fontFamily: 'monospace', width: '100%', boxSizing: 'border-box' }}
          />
        </Field>
        <button className="btn-primary" type="submit" disabled={submitting} style={{ justifySelf: 'start', padding: '0.5rem 1.5rem' }}>
          {submitting ? '提交中…' : '提交作业'}
        </button>
      </form>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' }}>
        <h3 style={{ margin: 0, fontSize: '1.05rem' }}>作业队列</h3>
        <button className="btn-primary" onClick={refresh} style={{ padding: '0.3rem 0.9rem' }}>刷新</button>
      </div>

      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', marginBottom: '0.75rem' }}>
        {FILTERS.map((f) => {
          const active = filter === f.value;
          return (
            <button
              key={f.value}
              onClick={() => setFilter(f.value)}
              style={{
                background: 'var(--card-bg)',
                boxShadow: 'var(--shadow-btn)',
                border: `1px solid ${active ? 'var(--accent-primary)' : 'transparent'}`,
                borderRadius: 8,
                padding: '0.3rem 0.8rem',
                fontSize: '0.8rem',
                fontWeight: 700,
                cursor: 'pointer',
                color: active ? 'var(--accent-primary)' : 'var(--text-main,#f1f5f9)',
                transition: 'border-color .2s ease, color .2s ease',
              }}
            >
              {f.label}
            </button>
          );
        })}
      </div>

      {loading ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>加载中…</div>
      ) : jobs.length === 0 ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>当前无作业。</div>
      ) : visibleJobs.length === 0 ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>无匹配条件的作业。</div>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.88rem' }}>
            <thead>
              <tr style={{ textAlign: 'left', color: 'var(--text-muted,#94a3b8)', borderBottom: '1px solid var(--border-color,#2a2f3a)' }}>
                <th style={th}>ID</th>
                <th style={th}>名称</th>
                <th style={th}>用户</th>
                <th style={th}>分区</th>
                <th style={th}>状态</th>
                <th style={th}>节点</th>
                <th style={th}>时限</th>
                <th style={th}>提交时间</th>
                <th style={th}>操作</th>
              </tr>
            </thead>
            <tbody>
              {visibleJobs.map((j) => (
                <tr key={j.job_id} style={{ borderBottom: '1px solid var(--border-color,#2a2f3a)' }}>
                  <td style={td}>{j.job_id}</td>
                  <td style={td}>{j.name}</td>
                  <td style={td}>{j.owner || '-'}</td>
                  <td style={td}>{j.partition}</td>
                  <td style={td}>
                    <span style={{ padding: '0.15rem 0.5rem', borderRadius: 6, fontSize: '0.72rem', fontWeight: 700, color: '#fff', background: jobStateColor(j.job_state) }}>
                      {j.job_state}
                    </span>
                  </td>
                  <td style={td}>{j.nodes || '-'}</td>
                  <td style={td}>{j.time_limit || '-'}</td>
                  <td style={td}>{j.submit_time ? new Date(j.submit_time * 1000).toLocaleString() : '-'}</td>
                  <td style={{ ...td, display: 'flex', gap: '0.4rem' }}>
                    <MiniBtn disabled={!!acting} onClick={() => act(j.job_id, 'cancel')}>取消</MiniBtn>
                    <MiniBtn disabled={!!acting} onClick={() => act(j.job_id, 'hold')}>挂起</MiniBtn>
                    <MiniBtn disabled={!!acting} onClick={() => act(j.job_id, 'requeue')}>重排</MiniBtn>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

const th = { padding: '0.5rem' } as const;
const td = { padding: '0.5rem' } as const;

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
      {label}
      {children}
    </label>
  );
}

function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{children}</div>;
}

function MiniBtn({ disabled, onClick, children }: { disabled?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      style={{
        padding: '0.25rem 0.6rem',
        fontSize: '0.75rem',
        borderRadius: 6,
        border: 'none',
        background: 'var(--card-bg)',
        boxShadow: 'var(--shadow-btn)',
        color: 'var(--text-main,#f1f5f9)',
        cursor: disabled ? 'wait' : 'pointer',
        transition: 'box-shadow .2s ease',
      }}
    >
      {children}
    </button>
  );
}
