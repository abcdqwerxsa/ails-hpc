import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useEffect, useState, type ReactNode } from 'react';
import { slurm, type JobSummary, type NodeStateInfo, type Partition } from '../services/slurm';

export const Route = createFileRoute('/')({ component: OverviewPage });

function OverviewPage() {
  const navigate = useNavigate();
  const [status, setStatus] = useState<'UP' | 'DEGRADED' | '…'>('…');
  const [release, setRelease] = useState('');
  const [nodes, setNodes] = useState<NodeStateInfo[]>([]);
  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [partitions, setPartitions] = useState<Partition[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    const tick = async () => {
      try {
        const p = await slurm.getClusterStatus();
        setStatus((p.pings?.[0]?.ping || '').toUpperCase() === 'UP' ? 'UP' : 'DEGRADED');
        setRelease(p.meta?.Slurm?.release || '');
        setError('');
      } catch {
        setStatus('DEGRADED');
        setError('slurmrestd 不可达（集群状态降级）');
      }
      try {
        const r = await slurm.getNodes();
        setNodes(r.nodes || []);
      } catch {
        /* 已由 status 反映 */
      }
      try {
        const j = await slurm.getJobs();
        setJobs(j.jobs || []);
      } catch {
        /* ignore */
      }
      try {
        const pr = await slurm.getPartitions();
        setPartitions(pr.partitions || []);
      } catch {
        /* ignore */
      }
    };
    tick();
    const t = setInterval(tick, 5000);
    return () => clearInterval(t);
  }, []);

  const idle = nodes.filter((n) => (n.state || '').toUpperCase() === 'IDLE').length;
  const drained = nodes.filter((n) => (n.state || '').toUpperCase().includes('DRAIN')).length;
  const cpuAlloc = nodes.reduce((s, n) => s + (n.alloc_cpus || 0), 0);
  const cpuTot = nodes.reduce((s, n) => s + (n.cpus || 0), 0);
  const memAlloc = nodes.reduce((s, n) => s + (n.alloc_memory || 0), 0);
  const memTot = nodes.reduce((s, n) => s + (n.real_memory || 0), 0);
  const running = jobs.filter((j) => (j.job_state || '').toUpperCase() === 'RUNNING').length;
  const pending = jobs.filter((j) => (j.job_state || '').toUpperCase() === 'PENDING').length;
  const held = jobs.filter((j) => (j.job_state || '').toUpperCase() === 'HELD').length;
  const completed = jobs.filter((j) => (j.job_state || '').toUpperCase() === 'COMPLETED').length;
  const failed = jobs.filter((j) => (j.job_state || '').toUpperCase() === 'FAILED').length;
  const cpuPct = cpuTot > 0 ? Math.round((cpuAlloc / cpuTot) * 100) : 0;
  const memPct = memTot > 0 ? Math.round((memAlloc / memTot) * 100) : 0;
  const gpuAlloc = nodes.reduce((s, n) => s + (n.alloc_gpus || 0), 0);
  const gpuTot = nodes.reduce((s, n) => s + (n.gpus || 0), 0);
  const gpuPct = gpuTot > 0 ? Math.round((gpuAlloc / gpuTot) * 100) : 0;

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1.5rem' }}>集群总览</h2>

      {error && <Notice color="#f59e0b" bg="rgba(245,158,11,.12)">{error}</Notice>}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '1rem', marginBottom: '1.5rem' }}>
        <Stat label="集群状态" value={status} color={status === 'UP' ? '#10b981' : status === 'DEGRADED' ? '#f59e0b' : '#888'} />
        <Stat label="节点总数" value={String(nodes.length)} />
        <Stat label="IDLE / DRAIN" value={`${idle} / ${drained}`} />
        <Stat label="作业总数" value={String(jobs.length)} />
        <Stat label="RUNNING / PENDING" value={`${running} / ${pending}`} color="#3b82f6" />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(260px,1fr))', gap: '1rem', marginBottom: '1.5rem' }}>
        <Gauge label="CPU 占用" alloc={cpuAlloc} tot={cpuTot} pct={cpuPct} unit="核" color="#3b82f6" />
        <Gauge label="内存占用" alloc={memAlloc} tot={memTot} pct={memPct} unit="MB" color="#10b981" />
        {gpuTot > 0 && <Gauge label="GPU 占用" alloc={gpuAlloc} tot={gpuTot} pct={gpuPct} unit="卡" color="#a855f7" />}
      </div>

      {release && <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.85rem' }}>Slurm 版本：{release}</div>}

      {/* 节点状态矩阵 */}
      <div style={{ background: 'var(--bg-card,#1b1e28)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 12, padding: '1.25rem', boxShadow: 'var(--shadow-card)', marginBottom: '1.5rem', marginTop: '1.5rem' }}>
        <h3 style={{ margin: '0 0 0.85rem', fontSize: '1rem' }}>节点状态（点击进入节点详情）</h3>
        {nodes.length === 0 ? (
          <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.85rem' }}>暂无节点数据</div>
        ) : (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(72px, 1fr))', gap: '0.5rem' }}>
            {nodes.map((n) => {
              const dot = stateColor(n.state);
              return (
                <div
                  key={n.name}
                  title={`${n.state} · 点击查看详情`}
                  onClick={() => navigate({ to: '/nodes' })}
                  style={{
                    background: stateTint(n.state),
                    boxShadow: 'var(--shadow-btn)',
                    borderRadius: 8,
                    padding: '0.4rem',
                    textAlign: 'center',
                    cursor: 'pointer',
                    transition: 'box-shadow .2s ease',
                    minHeight: 64,
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: '0.25rem',
                  }}
                >
                  <span style={{ width: 8, height: 8, borderRadius: '50%', background: dot, boxShadow: `0 0 6px ${dot}` }} />
                  <span style={{ fontSize: '0.72rem', fontWeight: 600, color: 'var(--text-main,#f1f5f9)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '100%' }}>{n.name}</span>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* 作业队列 */}
      <div style={{ background: 'var(--bg-card,#1b1e28)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 12, padding: '1.25rem', boxShadow: 'var(--shadow-card)', marginBottom: '1.5rem' }}>
        <h3 style={{ margin: '0 0 0.85rem', fontSize: '1rem' }}>作业队列</h3>
        {jobs.length === 0 ? (
          <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.85rem' }}>暂无作业</div>
        ) : (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.75rem' }}>
            <JobStat label="RUNNING" value={running} color="#10b981" />
            <JobStat label="PENDING" value={pending} color="#f59e0b" />
            <JobStat label="HELD" value={held} color="#f59e0b" />
            <JobStat label="COMPLETED" value={completed} color="#06b6d4" />
            <JobStat label="FAILED" value={failed} color="#f43f5e" />
          </div>
        )}
      </div>

      {/* 分区概览 */}
      <div className="table-card" style={{ marginBottom: '1.5rem' }}>
        <div className="table-header">
          <h3 style={{ margin: 0, fontSize: '1rem' }}>分区</h3>
        </div>
        <table className="custom-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>节点</th>
              <th>总CPU</th>
              <th>总节点数</th>
            </tr>
          </thead>
          <tbody>
            {partitions.length === 0 ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--text-muted,#94a3b8)' }}>暂无分区数据</td>
              </tr>
            ) : (
              partitions.map((p) => (
                <tr key={p.name}>
                  <td style={{ fontWeight: 600 }}>{p.name}</td>
                  <td style={{ color: 'var(--text-muted,#94a3b8)' }}>{p.nodes}</td>
                  <td>{p.total_cpus}</td>
                  <td>{p.total_nodes}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Stat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ background: 'var(--bg-card,#1b1e28)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 12, padding: '1.25rem', boxShadow: 'var(--shadow-card)', transition: 'box-shadow .3s ease' }}>
      <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.8rem', marginBottom: '0.5rem' }}>{label}</div>
      <div style={{ fontSize: '1.5rem', fontWeight: 700, color: color || 'var(--text-main,#f1f5f9)' }}>{value}</div>
    </div>
  );
}

function Gauge({ label, alloc, tot, pct, unit, color }: { label: string; alloc: number; tot: number; pct: number; unit: string; color: string }) {
  return (
    <div style={{ background: 'var(--bg-card,#1b1e28)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 12, padding: '1.25rem', boxShadow: 'var(--shadow-card)', transition: 'box-shadow .3s ease' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
        <span style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.85rem' }}>{label}</span>
        <span style={{ fontWeight: 700, color }}>{pct}%</span>
      </div>
      <div style={{ background: 'var(--bg-card-hover,#222632)', borderRadius: 6, height: 10, overflow: 'hidden', marginBottom: '0.4rem', boxShadow: 'var(--shadow-inset-deep)' }}>
        <div style={{ width: `${pct}%`, height: '100%', background: color, boxShadow: `0 0 8px ${color}`, transition: 'width .3s' }} />
      </div>
      <div style={{ fontSize: '0.78rem', color: 'var(--text-muted,#888)' }}>{alloc} / {tot} {unit} 分配</div>
    </div>
  );
}

function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{children}</div>;
}

// 节点状态 → 状态色（与 nodes.tsx 同源）：IDLE=emerald / DRAIN=amber / ALLOCATED·MIXED=blue / DOWN·FAIL=rose
function stateColor(state: string): string {
  const s = (state || '').toUpperCase();
  if (s.includes('DRAIN')) return '#f59e0b';
  if (s.includes('DOWN') || s.includes('FAIL')) return '#f43f5e';
  if (s.includes('ALLOCATED') || s.includes('MIXED')) return '#3b82f6';
  return '#10b981'; // IDLE
}

// 节点状态 → 芯片背景色（半透明 tint，叠加在 neumorphic 凸起阴影上）
function stateTint(state: string): string {
  const s = (state || '').toUpperCase();
  if (s.includes('DRAIN')) return 'rgba(245,158,11,.14)';
  if (s.includes('DOWN') || s.includes('FAIL')) return 'rgba(244,63,94,.14)';
  if (s.includes('ALLOCATED') || s.includes('MIXED')) return 'rgba(59,130,246,.14)';
  return 'rgba(16,185,129,.14)'; // IDLE
}

function JobStat({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div style={{ background: 'var(--card-bg)', boxShadow: 'var(--shadow-btn)', borderRadius: 8, padding: '0.6rem 0.9rem', display: 'flex', alignItems: 'center', gap: '0.5rem', minWidth: 120 }}>
      <span style={{ width: 8, height: 8, borderRadius: '50%', background: color, boxShadow: `0 0 6px ${color}`, flexShrink: 0 }} />
      <span style={{ fontSize: '0.78rem', color: 'var(--text-muted,#94a3b8)' }}>{label}</span>
      <strong style={{ marginLeft: 'auto', fontSize: '1rem', color }}>{value}</strong>
    </div>
  );
}
