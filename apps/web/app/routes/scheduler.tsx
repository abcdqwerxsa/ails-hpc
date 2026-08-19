import { createFileRoute } from '@tanstack/react-router';
import { Fragment, useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { slurm, type Partition, type PartitionDetail, type UpdatePartitionRequest } from '../services/slurm';
import { can, getStoredUser } from '../services/auth';
import { QOSPanel, ReservationsPanel } from '../components/scheduler_sections';

// 2026-08-19 IA 重组：原「分区」页扩为「调度管理」——分区/预约/QOS 同属 Slurm 调度器
// 配置三件套（预约与 QOS 面板自 admin.tsx 迁入）。
export const Route = createFileRoute('/scheduler')({ component: SchedulerPage });

// 分区编辑表单（空串=不变更；下拉含空选项"不变"）
const EMPTY_EDIT = {
  state: '', default: '', maxTime: '', defMemPerCPU: '', overSubscribe: '',
  nodes: '', allowAccounts: '', allowGroups: '',
};

function SchedulerPage() {
  const [parts, setParts] = useState<Partition[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [editing, setEditing] = useState(''); // 正在编辑的分区名

  const canManage = can('partitions:manage', getStoredUser());

  const reload = async () => {
    try {
      const r = await slurm.getPartitions();
      setParts(r.partitions || []);
      setError('');
    } catch (e: any) {
      setError(e?.message || '加载分区失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    reload();
  }, []);

  // Real data only: Partition 暴露 total_cpus / total_nodes，无 alloc 字段。
  // 汇总卡取合计（注意：节点可属多个分区，故合计可能 > 集群实际总数）；行内进度条按
  // "该分区 CPU 占分区 CPU 合计的比例"绘制（占比，非真实利用率）。
  const totalCpus = parts.reduce((s, p) => s + (p.total_cpus || 0), 0);
  const totalNodes = parts.reduce((s, p) => s + (p.total_nodes || 0), 0);

  return (
    <div className="partitions-page">
      <style>{`
        .partitions-page .pt-row { transition: background-color .2s ease; }
        .partitions-page .pt-row:hover { background: var(--bg-card-hover, #222632); }
      `}</style>

      <h2 style={{ marginTop: 0, marginBottom: '1.5rem' }}>调度管理</h2>

      {error && <Notice color="#f43f5e" bg="rgba(244,63,94,.12)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.12)">{info}</Notice>}

      <h3 style={{ margin: '0 0 1rem', fontSize: '1.1rem' }}>分区</h3>

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))',
          gap: '1rem',
          marginBottom: '1.5rem',
        }}
      >
        <Stat label="分区总数" value={loading ? '—' : String(parts.length)} />
        <Stat label="分区 CPU 合计" value={loading ? '—' : String(totalCpus)} />
        <Stat label="分区节点合计" value={loading ? '—' : String(totalNodes)} />
        <Stat
          label="分区 GPU 合计"
          value={loading ? '—' : String(parts.reduce((s, p) => s + (p.gpus || 0), 0))}
          color="var(--accent-violet,#A855F7)"
        />
      </div>

      {loading ? (
        <div
          className="table-card"
          style={{ padding: '2.5rem', textAlign: 'center', color: 'var(--text-muted,#94a3b8)' }}
        >
          加载中…
        </div>
      ) : parts.length === 0 ? (
        <div
          className="table-card"
          style={{ padding: '2.5rem', textAlign: 'center', color: 'var(--text-muted,#94a3b8)' }}
        >
          暂无分区数据
        </div>
      ) : (
        <div className="table-card">
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
              <thead>
                <tr style={{ textAlign: 'left' }}>
                  <th style={th}>名称</th>
                  <th style={th}>节点</th>
                  <th style={th}>总 CPU</th>
                  <th style={th}>总内存</th>
                  <th style={th}>GPU</th>
                  <th style={th}>总节点数</th>
                  <th style={th}>集群 CPU 占比</th>
                  {canManage && <th style={th}>操作</th>}
                </tr>
              </thead>
              <tbody>
                {parts.map((p, i) => {
                  const pct = totalCpus > 0 ? Math.round(((p.total_cpus || 0) / totalCpus) * 100) : 0;
                  const isLast = i === parts.length - 1;
                  const rowBorder = isLast && editing !== p.name ? 'none' : '1px solid var(--border-color,#2a2f3a)';
                  return (
                    <Fragment key={p.name}>
                      <tr
                        className="pt-row"
                        style={{
                          borderBottom: rowBorder,
                        }}
                      >
                        <td style={td}>
                          <span style={{ fontWeight: 700, color: 'var(--text-main,#f1f5f9)' }}>{p.name}</span>
                        </td>
                        <td
                          style={{
                            ...td,
                            fontFamily: "'JetBrains Mono', monospace",
                            fontSize: '0.8rem',
                            color: 'var(--text-muted,#94a3b8)',
                          }}
                        >
                          {p.nodes || '-'}
                        </td>
                        <td style={{ ...td, fontFamily: "'JetBrains Mono', monospace", fontWeight: 700 }}>
                          {p.total_cpus}
                        </td>
                        <td style={{ ...td, fontFamily: "'JetBrains Mono', monospace" }}>
                          {p.total_memory_mb ? `${(p.total_memory_mb / 1024).toFixed(1)} GB` : '—'}
                        </td>
                        <td style={{ ...td, fontFamily: "'JetBrains Mono', monospace", fontWeight: 700 }}>
                          {p.gpus ? (
                            <span style={{ color: 'var(--accent-violet,#A855F7)' }}>
                              {p.alloc_gpus ?? 0}/{p.gpus} 卡
                            </span>
                          ) : (
                            <span style={{ color: 'var(--text-muted,#94a3b8)' }}>—</span>
                          )}
                        </td>
                        <td style={{ ...td, fontFamily: "'JetBrains Mono', monospace", fontWeight: 700 }}>
                          {p.total_nodes}
                        </td>
                        <td style={td}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '0.6rem' }}>
                            <div
                              style={{
                                flex: 1,
                                maxWidth: 140,
                                height: 8,
                                background: 'var(--bg-card-hover,#222632)',
                                borderRadius: 6,
                                overflow: 'hidden',
                                boxShadow: 'var(--shadow-inset-deep)',
                              }}
                            >
                              <div
                                style={{
                                  width: `${pct}%`,
                                  height: '100%',
                                  background: 'var(--accent-cyan,#06B6D4)',
                                  boxShadow: 'var(--accent-cyan-glow)',
                                  transition: 'width .3s ease',
                                }}
                              />
                            </div>
                            <span
                              style={{
                                fontSize: '0.75rem',
                                color: 'var(--text-muted,#94a3b8)',
                                fontFamily: "'JetBrains Mono', monospace",
                                minWidth: '2.5rem',
                              }}
                            >
                              {pct}%
                            </span>
                          </div>
                        </td>
                        {canManage && (
                          <td style={td}>
                            <MiniBtn onClick={() => setEditing(editing === p.name ? '' : p.name)}>
                              {editing === p.name ? '收起' : '编辑'}
                            </MiniBtn>
                          </td>
                        )}
                      </tr>
                      {editing === p.name && (
                        <tr style={{ background: 'var(--bg-card-hover,#222632)' }}>
                          <td colSpan={canManage ? 8 : 7} style={{ padding: '1rem 1.25rem 1.25rem' }}>
                            <PartitionEditor
                              name={p.name}
                              onDone={async () => {
                                setEditing('');
                                setInfo(`分区 ${p.name} 已更新（Slurm 生效）`);
                                await reload();
                              }}
                              onCancel={() => setEditing('')}
                            />
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* 预约 / QOS（自 admin.tsx 迁入；各自权限门） */}
      {can('reservations:manage', getStoredUser()) && <ReservationsPanel />}
      {can('qos:manage', getStoredUser()) && <QOSPanel />}
    </div>
  );
}

// PartitionEditor 行内分区编辑器：展开时拉取 scontrol 当前值（placeholder 展示），
// 留空字段不变更；提交 PATCH /admin/partitions/:name（partitions:manage）。
function PartitionEditor({
  name,
  onDone,
  onCancel,
}: {
  name: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [detail, setDetail] = useState<PartitionDetail | null>(null);
  const [form, setForm] = useState(EMPTY_EDIT);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    (async () => {
      try {
        const r = await slurm.getPartition(name);
        setDetail(r.partition || null);
      } catch (e: any) {
        setErr(e?.message || '读取分区当前属性失败（仍可直接修改）');
      }
    })();
  }, [name]);

  const cur = (v?: string) => (v ? `当前 ${v}` : '当前未设置');

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const payload: UpdatePartitionRequest = {};
    if (form.state.trim()) payload.state = form.state.trim();
    if (form.default.trim()) payload.default = form.default.trim();
    if (form.maxTime.trim()) payload.maxTime = form.maxTime.trim();
    if (form.defMemPerCPU.trim()) payload.defMemPerCPU = form.defMemPerCPU.trim();
    if (form.overSubscribe.trim()) payload.overSubscribe = form.overSubscribe.trim();
    if (form.nodes.trim()) payload.nodes = form.nodes.trim();
    if (form.allowAccounts.trim()) payload.allowAccounts = form.allowAccounts.trim();
    if (form.allowGroups.trim()) payload.allowGroups = form.allowGroups.trim();
    if (Object.keys(payload).length === 0) {
      setErr('至少填写一个要修改的字段（留空=不变更）');
      return;
    }
    setBusy(true);
    setErr('');
    try {
      await slurm.updatePartition(name, payload);
      onDone();
    } catch (e: any) {
      setErr(e?.message || '更新失败');
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: '0.75rem' }}>
      <div style={{ fontSize: '0.9rem', fontWeight: 700 }}>
        修改分区 {name}
        <span style={{ marginLeft: '0.75rem', fontSize: '0.78rem', fontWeight: 400, color: 'var(--text-muted,#94a3b8)' }}>
          留空 = 不变更；当前值显示在输入框内
        </span>
      </div>
      {err && <Notice color="#f43f5e" bg="rgba(244,63,94,.12)">{err}</Notice>}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: '0.75rem' }}>
        <Field label={`State（${detail ? cur(detail.state) : '…'}）`}>
          <select
            className="form-control"
            value={form.state}
            onChange={(e) => setForm({ ...form, state: e.target.value })}
          >
            <option value="">不变更</option>
            {['UP', 'DOWN', 'DRAIN', 'INACTIVE'].map((s) => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
        </Field>
        <Field label={`Default（${detail ? cur(detail.default) : '…'}）`}>
          <select
            className="form-control"
            value={form.default}
            onChange={(e) => setForm({ ...form, default: e.target.value })}
          >
            <option value="">不变更</option>
            <option value="YES">YES</option>
            <option value="NO">NO</option>
          </select>
        </Field>
        <Field label={`OverSubscribe（${detail ? cur(detail.overSubscribe) : '…'}）`}>
          <select
            className="form-control"
            value={form.overSubscribe}
            onChange={(e) => setForm({ ...form, overSubscribe: e.target.value })}
          >
            <option value="">不变更</option>
            {['YES', 'NO', 'EXCLUSIVE', 'FORCE', 'FORCE:1'].map((s) => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
        </Field>
        <Field label={`MaxTime（${detail ? cur(detail.maxTime) : '…'}）`}>
          <input
            className="form-control"
            value={form.maxTime}
            onChange={(e) => setForm({ ...form, maxTime: e.target.value })}
            placeholder="如 1-00:00:00 / 60 / unlimited"
          />
        </Field>
        <Field label={`DefMemPerCPU（${detail ? cur(detail.defMemPerCPU) : '…'}）`}>
          <input
            className="form-control"
            value={form.defMemPerCPU}
            onChange={(e) => setForm({ ...form, defMemPerCPU: e.target.value })}
            placeholder="如 4096 / 4G / unlimited"
          />
        </Field>
        <Field label={`Nodes（${detail ? cur(detail.nodes) : '…'}）`}>
          <input
            className="form-control"
            value={form.nodes}
            onChange={(e) => setForm({ ...form, nodes: e.target.value })}
            placeholder="如 c1,c2[3-5]"
          />
        </Field>
        <Field label={`AllowAccounts（${detail ? cur(detail.allowAccounts) : '…'}）`}>
          <input
            className="form-control"
            value={form.allowAccounts}
            onChange={(e) => setForm({ ...form, allowAccounts: e.target.value })}
            placeholder="如 ALL / ails_hpc_lab"
          />
        </Field>
        <Field label={`AllowGroups（${detail ? cur(detail.allowGroups) : '…'}）`}>
          <input
            className="form-control"
            value={form.allowGroups}
            onChange={(e) => setForm({ ...form, allowGroups: e.target.value })}
            placeholder="如 ALL / hpc_users"
          />
        </Field>
      </div>
      <div style={{ display: 'flex', gap: '0.75rem' }}>
        <button className="btn-primary" type="submit" disabled={busy} style={{ padding: '0.45rem 1.4rem' }}>
          {busy ? '提交中…' : '应用修改'}
        </button>
        <button
          type="button"
          onClick={onCancel}
          style={{
            padding: '0.45rem 1.4rem', borderRadius: 8, border: 'none', cursor: 'pointer',
            background: 'var(--bg-card,#1b1e28)', color: 'var(--text-main,#f1f5f9)',
            boxShadow: 'var(--shadow-btn)',
          }}
        >
          取消
        </button>
      </div>
    </form>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
      {label}
      {children}
    </label>
  );
}

const th = {
  padding: '0.85rem 1.25rem',
  fontSize: '0.72rem',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  color: 'var(--text-muted,#94a3b8)',
  fontWeight: 700,
  borderBottom: '1px solid var(--border-color,#2a2f3a)',
} as const;

const td = {
  padding: '0.9rem 1.25rem',
  fontSize: '0.875rem',
  color: 'var(--text-main,#f1f5f9)',
} as const;

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

function Stat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div
      style={{
        background: 'var(--bg-card,#1b1e28)',
        border: '1px solid var(--border-color,#2a2f3a)',
        borderRadius: 12,
        padding: '1.25rem',
        boxShadow: 'var(--shadow-card)',
        transition: 'box-shadow .3s ease',
      }}
    >
      <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.8rem', marginBottom: '0.5rem' }}>{label}</div>
      <div
        style={{
          fontSize: '1.5rem',
          fontWeight: 700,
          fontFamily: "'JetBrains Mono', monospace",
          color: color || 'var(--text-main,#f1f5f9)',
        }}
      >
        {value}
      </div>
    </div>
  );
}

function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return (
    <div
      style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}
    >
      {children}
    </div>
  );
}
