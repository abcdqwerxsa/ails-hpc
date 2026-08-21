import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useEffect, useState, type ChangeEvent, type FormEvent } from 'react';
import { slurm, type JobDetail, type JobSummary, type Partition, type QOSInfo } from '../services/slurm';
import { can } from '../services/auth';
import { Select } from '../components/select';
import { Field, MiniBtn, NeuSegmented, Notice, StatusBadge, th, td } from '../components/panel_ui';

export const Route = createFileRoute('/jobs')({ component: JobsPage });

export function QOSBadge({ qos }: { qos?: string }) {
  const name = (qos || 'normal').toLowerCase();
  let bg = 'rgba(6, 182, 212, 0.12)';
  let color = 'var(--accent-cyan,#06b6d4)';
  let border = 'rgba(6, 182, 212, 0.25)';

  if (name.includes('vip') || name.includes('high') || name.includes('prio')) {
    bg = 'rgba(245, 158, 11, 0.15)';
    color = '#f59e0b';
    border = 'rgba(245, 158, 11, 0.3)';
  } else if (name.includes('debug') || name.includes('test')) {
    bg = 'rgba(16, 185, 129, 0.15)';
    color = '#34d399';
    border = 'rgba(16, 185, 129, 0.3)';
  } else if (name.includes('gpu')) {
    bg = 'rgba(168, 85, 247, 0.15)';
    color = '#c084fc';
    border = 'rgba(168, 85, 247, 0.3)';
  }

  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.3rem',
        padding: '0.15rem 0.5rem',
        borderRadius: 6,
        fontSize: '0.75rem',
        fontFamily: "'JetBrains Mono', monospace",
        fontWeight: 700,
        background: bg,
        color,
        border: `1px solid ${border}`,
      }}
    >
      <span style={{ width: 6, height: 6, borderRadius: '50%', background: color }} />
      {qos || 'normal'}
    </span>
  );
}

const emptyForm = {
  name: '',
  partition: 'standard',
  memory_mb: '',
  gpus: '0',
  nodes: '1',
  tasks: '1',
  time_limit: '60',
  script: '#!/bin/bash\nsleep 30\n',
  qos: 'normal',
};

// 常用模板（v3-P1）：一键填充表单后按需修改。
const TEMPLATES: { label: string; hint: string; form: typeof emptyForm }[] = [
  {
    label: 'CPU 小任务',
    hint: 'standard（E 核）· 30 分钟 · 不申请 GPU · normal QOS',
    form: {
      ...emptyForm,
      name: 'cpu-demo',
      time_limit: '30',
      qos: 'normal',
      script: '#!/bin/bash\n# CPU 小任务模板：E 核分区，适合编译 / 数据处理 / 轻量计算\nsrun hostname\n# python preprocess.py\n',
    },
  },
  {
    label: '单卡 PyTorch',
    hint: 'performance（P 核）· 1 GPU · 4 小时',
    form: {
      ...emptyForm,
      name: 'pytorch-train',
      partition: 'performance',
      gpus: '1',
      time_limit: '240',
      qos: 'normal',
      script: '#!/bin/bash\n# 单卡 PyTorch 训练模板：P 核分区 + 1 GPU\n# 数据与代码建议放 /shared（节点间可见）\ncd /shared/$USER\npython -u train.py --epochs 10 --batch-size 32\n',
    },
  },
  {
    label: '长时批处理',
    hint: 'standard（E 核）· 24 小时 · 并发子任务',
    form: {
      ...emptyForm,
      name: 'batch-sweep',
      time_limit: '1440',
      qos: 'normal',
      script: '#!/bin/bash\n# 长时 CPU 批处理模板：参数扫描 / 批量后处理\nfor i in $(seq 1 100); do\n  srun -n1 ./worker.sh $i &\ndone\nwait\n',
    },
  },
];

// 作业状态过滤选项（值对齐 job_state 大写形式）
const FILTERS = [
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
  const [detail, setDetail] = useState<JobDetail | null>(null);
  const [detailErr, setDetailErr] = useState('');
  const [detailLoading, setDetailLoading] = useState(false);
  const [partitions, setPartitions] = useState<Partition[]>([]);
  const [availableQosList, setAvailableQosList] = useState<QOSInfo[]>([]);
  const [defaultQos, setDefaultQos] = useState<string>('normal');
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [arraySpec, setArraySpec] = useState('');
  const [dependency, setDependency] = useState('');

  // 分区列表与可用 QOS 发现
  useEffect(() => {
    slurm
      .getPartitions()
      .then((r) => setPartitions(r.partitions || []))
      .catch(() => {});

    slurm
      .getAvailableQOS()
      .then((r) => {
        const list =
          r.allowedQos && r.allowedQos.length > 0
            ? r.allowedQos
            : r.allowedQOS && r.allowedQOS.length > 0
            ? r.allowedQOS
            : r.availableQos && r.availableQos.length > 0
            ? r.availableQos
            : [{ name: 'normal', description: '标准调度策略' }];
        setAvailableQosList(list);
        const def = r.defaultQos || r.defaultQOS || list[0]?.name || 'normal';
        setDefaultQos(def);
        setForm((f) => ({ ...f, qos: f.qos || def }));
      })
      .catch(() => {
        setAvailableQosList([{ name: 'normal', description: '标准调度策略' }]);
      });
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
        memory_mb: Number(form.memory_mb) > 0 ? Number(form.memory_mb) : undefined,
        gpus: Number(form.gpus) > 0 ? Number(form.gpus) : undefined,
        array_spec: arraySpec.trim() || undefined,
        dependency: dependency.trim() || undefined,
        nodes: Number(form.nodes) || 1,
        tasks: Number(form.tasks) || 1,
        time_limit: String(form.time_limit || '60'),
        script: form.script,
        qos: form.qos?.trim() || undefined,
      });
      setInfo(`已提交：作业 #${r.job_id}${form.qos ? ` (QOS: ${form.qos})` : ''}`);
      setForm({ ...emptyForm, qos: defaultQos });
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

  const openDetail = async (id: number) => {
    setDetailLoading(true);
    setDetailErr('');
    setDetail(null);
    try {
      setDetail(await slurm.getJobDetail(id));
    } catch (e: any) {
      setDetailErr(e?.message || '加载详情失败');
    } finally {
      setDetailLoading(false);
    }
  };

  const field = (k: keyof typeof emptyForm) => (e: ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value });

  // 按状态过滤渲染的作业
  const visibleJobs = jobs.filter((j) => filter === 'ALL' || (j.job_state || '').toUpperCase() === filter);

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.5rem' }}>
        <h2 style={{ margin: 0 }}>作业管理</h2>
        <button className="btn-primary" onClick={refresh} style={{ padding: '0.45rem 1rem' }}>刷新</button>
      </div>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.12)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.12)">{info}</Notice>}

      {can('jobs:submit') && (
        <form
          onSubmit={submit}
          className="neu-chiseled-card"
          style={{ padding: '1.35rem', marginBottom: '1.75rem', display: 'grid', gap: '0.9rem' }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.65rem', flexWrap: 'wrap' }}>
            <span style={{ fontSize: '0.8rem', fontWeight: 600, color: 'var(--text-muted)' }}>常用模板</span>
            {TEMPLATES.map((tpl) => (
              <button
                key={tpl.label}
                type="button"
                className="neu-btn"
                title={tpl.hint}
                style={{ fontSize: '0.78rem', padding: '0.3rem 0.75rem' }}
                onClick={() => {
                  setForm(tpl.form);
                  setArraySpec('');
                  setDependency('');
                  setInfo(`已套用模板「${tpl.label}」（${tpl.hint}）——按需修改后提交`);
                }}
              >
                {tpl.label}
              </button>
            ))}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(150px,1fr))', gap: '0.85rem' }}>
            <Field label="作业名"><input className="form-control" value={form.name} onChange={field('name')} placeholder="my-job" /></Field>
            <Field label="分区">
              <Select
                value={form.partition}
                onChange={(v) =>
                  setForm({ ...form, partition: v, gpus: v === 'performance' ? form.gpus : '0' })
                }
                options={(partitions.length > 0 ? partitions.map((p) => p.name) : ['standard']).map((name) => ({
                  value: name,
                  label: `${name}${name === 'performance' ? '（P 核）' : name === 'standard' ? '（E 核）' : ''}`,
                }))}
              />
            </Field>
            <Field label="QOS 策略">
              <Select
                value={form.qos}
                onChange={(v) => setForm({ ...form, qos: v })}
                options={availableQosList.map((q) => ({
                  value: q.name,
                  label: `${q.name}${q.description ? ` (${q.description})` : ''}${q.priority ? ` · 优先级 ${q.priority}` : ''}`,
                }))}
              />
            </Field>
            <Field label="节点数"><input className="form-control" type="number" min="1" value={form.nodes} onChange={field('nodes')} /></Field>
            <Field label="任务数"><input className="form-control" type="number" min="1" value={form.tasks} onChange={field('tasks')} /></Field>
            <Field label="时限(分钟)"><input className="form-control" value={form.time_limit} onChange={field('time_limit')} /></Field>
            <Field label="内存 MB（可选）">
              <input
                className="form-control"
                type="number"
                min="0"
                max="6000"
                value={form.memory_mb}
                onChange={field('memory_mb')}
                placeholder="默认 350/核"
              />
            </Field>
            <Field label="GPU 卡数">
              <Select
                value={form.gpus}
                onChange={(v) =>
                  setForm({
                    ...form,
                    gpus: v,
                    partition: Number(v) > 0 ? 'performance' : form.partition,
                  })
                }
                options={[
                  { value: '0', label: '0（不申请）' },
                  { value: '1', label: '1 卡（P 核分区）' },
                ]}
              />
            </Field>
          </div>

          {/* QOS 配额提示卡 */}
          {(() => {
            const curQos = availableQosList.find((q) => q.name === form.qos);
            if (!curQos) return null;
            return (
              <div
                style={{
                  background: 'var(--bg-card-hover, rgba(255,255,255,0.03))',
                  border: '1px solid var(--border-color,#2a2f3a)',
                  borderRadius: 8,
                  padding: '0.65rem 0.9rem',
                  fontSize: '0.78rem',
                  display: 'flex',
                  gap: '1.1rem',
                  flexWrap: 'wrap',
                  alignItems: 'center',
                  color: 'var(--text-muted,#94a3b8)',
                }}
              >
                <div style={{ color: 'var(--text-main,#f1f5f9)', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '0.35rem' }}>
                  <span>QOS 配额策略：</span>
                  <QOSBadge qos={curQos.name} />
                </div>
                {curQos.priority && (
                  <div>🚀 优先级: <span style={{ color: 'var(--accent-cyan,#06b6d4)', fontWeight: 700 }}>{curQos.priority}</span></div>
                )}
                {(curQos.max_wall || curQos.max_wall_duration) && (
                  <div>⏱️ 限时: <span style={{ color: 'var(--text-main,#f1f5f9)', fontWeight: 600 }}>{curQos.max_wall_duration || curQos.max_wall}</span></div>
                )}
                {(curQos.max_tres_per_user || curQos.max_tres) && (
                  <div>⚡ 单人上限: <span style={{ color: 'var(--text-main,#f1f5f9)', fontWeight: 600 }}>{curQos.max_tres_per_user || curQos.max_tres}</span></div>
                )}
                {(curQos.max_jobs_per_user || curQos.max_jobs) && (
                  <div>📊 并发作业: <span style={{ color: '#f59e0b', fontWeight: 600 }}>{curQos.max_jobs_per_user || curQos.max_jobs}</span></div>
                )}
                {curQos.description && (
                  <div style={{ color: 'var(--text-dim,#64748b)', fontStyle: 'italic' }}>({curQos.description})</div>
                )}
              </div>
            );
          })()}

          <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
            <button type="button" className="neu-btn" onClick={() => setShowAdvanced(!showAdvanced)}>
              {showAdvanced ? '收起高级选项' : '高级选项（数组/依赖）'}
            </button>
            {arraySpec.trim() && <span style={{ fontSize: '0.78rem', color: 'var(--accent-primary)' }}>数组 {arraySpec.trim()}</span>}
            {dependency.trim() && <span style={{ fontSize: '0.78rem', color: 'var(--accent-amber,#f59e0b)' }}>依赖 {dependency.trim()}</span>}
          </div>
          {showAdvanced && (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '0.75rem' }}>
              <Field label="作业数组（可选）">
                <input className="form-control" value={arraySpec} onChange={(e) => setArraySpec(e.target.value)} placeholder="如 1-4 或 1-10%2" />
              </Field>
              <Field label="依赖（可选）">
                <input className="form-control" value={dependency} onChange={(e) => setDependency(e.target.value)} placeholder="如 afterok:123" />
              </Field>
            </div>
          )}
          <Field label="脚本">
            <textarea
              className="form-control"
              rows={4}
              value={form.script}
              onChange={(e) => setForm({ ...form, script: e.target.value })}
              style={{ fontFamily: 'monospace', width: '100%', boxSizing: 'border-box' }}
            />
          </Field>
          <button className="btn-primary" type="submit" disabled={submitting} style={{ justifySelf: 'start', padding: '0.55rem 1.6rem' }}>
            {submitting ? '提交中…' : '提交作业'}
          </button>
        </form>
      )}

      {/* 作业队列头部与拟物分段控制器 */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1rem', flexWrap: 'wrap', gap: '0.75rem' }}>
        <h3 style={{ margin: 0, fontSize: '1.05rem', fontWeight: 700 }}>作业队列</h3>
        <NeuSegmented options={FILTERS} value={filter} onChange={(v) => setFilter(v)} />
      </div>

      {loading ? (
        <div style={{ color: 'var(--text-muted)', padding: '2rem 0' }}>加载中…</div>
      ) : jobs.length === 0 ? (
        <div style={{ color: 'var(--text-muted)', padding: '2rem 0' }}>当前无作业。</div>
      ) : visibleJobs.length === 0 ? (
        <div style={{ color: 'var(--text-muted)', padding: '2rem 0' }}>无匹配条件的作业。</div>
      ) : (
        <div className="neu-chiseled-card" style={{ overflowX: 'auto', marginBottom: '1.75rem' }}>
          <table className="custom-table">
            <thead>
              <tr>
                <th style={th}>ID</th>
                <th style={th}>名称</th>
                <th style={th}>用户</th>
                <th style={th}>分区</th>
                <th style={th}>QOS</th>
                <th style={th}>状态</th>
                <th style={th}>节点</th>
                <th style={th}>时限</th>
                <th style={th}>提交时间</th>
                <th style={th}>操作</th>
              </tr>
            </thead>
            <tbody>
              {visibleJobs.map((j) => (
                <tr key={j.job_id}>
                  <td style={{ ...td, fontFamily: "'JetBrains Mono', monospace", fontWeight: 700 }}>{j.job_id}</td>
                  <td style={{ ...td, fontWeight: 600 }}>{j.name}</td>
                  <td style={td}>{j.owner || '-'}</td>
                  <td style={td}>{j.partition}</td>
                  <td style={td}>
                    <QOSBadge qos={j.qos || 'normal'} />
                  </td>
                  <td style={td}>
                    <StatusBadge status={j.job_state} />
                  </td>
                  <td style={{ ...td, fontFamily: "'JetBrains Mono', monospace" }}>{j.nodes || '-'}</td>
                  <td style={{ ...td, fontFamily: "'JetBrains Mono', monospace" }}>{j.time_limit || '-'}</td>
                  <td style={{ ...td, fontSize: '0.8rem', color: 'var(--text-muted)' }}>{j.submit_time ? new Date(j.submit_time * 1000).toLocaleString() : '-'}</td>
                  <td style={{ ...td, display: 'flex', gap: '0.45rem' }}>
                    <MiniBtn onClick={() => openDetail(j.job_id)}>详情</MiniBtn>
                    {can('jobs:control') && (
                      <>
                        <MiniBtn disabled={acting.startsWith(String(j.job_id))} onClick={() => act(j.job_id, 'cancel')}>取消</MiniBtn>
                        <MiniBtn disabled={acting.startsWith(String(j.job_id))} onClick={() => act(j.job_id, 'hold')}>挂起</MiniBtn>
                        <MiniBtn disabled={acting.startsWith(String(j.job_id))} onClick={() => act(j.job_id, 'requeue')}>重排</MiniBtn>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* 作业详情弹窗 */}
      {(detail || detailLoading || detailErr) && (
        <div
          onClick={() => { setDetail(null); setDetailErr(''); setDetailLoading(false); }}
          style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.55)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000, padding: '1.5rem' }}
        >
          <div
            onClick={(e) => e.stopPropagation()}
            className="neu-chiseled-card"
            style={{ maxWidth: 720, width: '100%', maxHeight: '85vh', overflowY: 'auto', padding: '1.5rem' }}
          >
            {detailLoading && <div style={{ padding: '2rem', textAlign: 'center', color: 'var(--text-muted)' }}>详情加载中…</div>}
            {detailErr && <Notice color="#f43f5e" bg="rgba(239,68,68,.12)">{detailErr}</Notice>}
            {detail && (
              <>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.25rem' }}>
                  <div style={{ fontSize: '1.1rem', fontWeight: 700, display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
                    <span>作业 #{detail.job_id} · {detail.name}</span>
                    <StatusBadge status={detail.state} />
                  </div>
                  <MiniBtn onClick={() => setDetail(null)}>关闭</MiniBtn>
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(150px,1fr))', gap: '0.75rem', marginBottom: '1.25rem' }}>
                  <DetailKV k="属主" v={detail.owner} />
                  <DetailKV k="账户" v={detail.account} />
                  <DetailKV k="分区" v={detail.partition} />
                  <DetailKV k="QOS 策略" v={detail.qos || 'normal'} />
                  <DetailKV k="耗时" v={detail.elapsed_sec != null ? `${Math.floor(detail.elapsed_sec / 60)}m${detail.elapsed_sec % 60}s` : undefined} />
                  <DetailKV k="退出码" v={detail.exit_code} />
                  <DetailKV k="提交" v={detail.submit} />
                  <DetailKV k="开始" v={detail.start} />
                  <DetailKV k="结束" v={detail.end} />
                </div>
                <div style={{ fontSize: '0.85rem', fontWeight: 700, marginBottom: '0.5rem' }}>输出（末 200 行）</div>
                <pre
                  style={{
                    margin: 0, padding: '0.85rem', borderRadius: 8, fontSize: '0.78rem', lineHeight: 1.5,
                    fontFamily: "'JetBrains Mono', monospace", whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                    background: 'var(--terminal-bg, #0f172a)', color: '#34d399',
                    maxHeight: 320, overflowY: 'auto', boxShadow: 'var(--shadow-inset-deep)',
                  }}
                >
                  {detail.stdout_tail || '（暂无输出——作业可能尚未运行或输出为空）'}
                </pre>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function DetailKV({ k, v }: { k: string; v?: string }) {
  return (
    <div style={{ background: 'var(--card-bg)', padding: '0.5rem 0.75rem', borderRadius: 8, boxShadow: 'var(--shadow-inset-deep)' }}>
      <div style={{ fontSize: '0.72rem', color: 'var(--text-muted)', marginBottom: '0.2rem' }}>{k}</div>
      <div style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.85rem', fontWeight: 600, color: 'var(--text-main)' }}>{v || '-'}</div>
    </div>
  );
}
