import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { slurm, type NodeStateInfo, type JobSummary, type Partition } from '../services/slurm';

export const Route = createFileRoute('/monitor')({ component: MonitorPage });

function MonitorPage() {
  const [status, setStatus] = useState<'UP' | 'DEGRADED' | '…'>('…');
  const [release, setRelease] = useState('');
  const [nodes, setNodes] = useState<NodeStateInfo[]>([]);
  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [parts, setParts] = useState<Partition[]>([]);
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
        const pa = await slurm.getPartitions();
        setParts(pa.partitions || []);
      } catch {
        /* ignore */
      }
    };
    tick();
    const t = setInterval(tick, 5000);
    return () => clearInterval(t);
  }, []);

  // --- 聚合（全部由真实数据 reduce 而来，空数据兜底为 0） ---
  const totalCpus = nodes.reduce((s, n) => s + (n.cpus || 0), 0);
  const totalGpus = nodes.reduce((s, n) => s + (n.gpus || 0), 0);

  const cpuAlloc = nodes.reduce((s, n) => s + (n.alloc_cpus || 0), 0);
  const cpuPct = totalCpus > 0 ? Math.round((cpuAlloc / totalCpus) * 100) : 0;

  const memAlloc = nodes.reduce((s, n) => s + (n.alloc_memory || 0), 0);
  const memTot = nodes.reduce((s, n) => s + (n.real_memory || 0), 0);
  const memPct = memTot > 0 ? Math.round((memAlloc / memTot) * 100) : 0;

  const gpuAlloc = nodes.reduce((s, n) => s + (n.alloc_gpus || 0), 0);
  const gpuPct = totalGpus > 0 ? Math.round((gpuAlloc / totalGpus) * 100) : 0;

  // 节点状态分布：按归一化桶分类后计数
  const dist = nodes.reduce<Record<string, number>>((acc, n) => {
    const b = nodeBucket(n.state);
    acc[b] = (acc[b] || 0) + 1;
    return acc;
  }, {});
  const distEntries = Object.entries(dist).sort((a, b) => b[1] - a[1]);

  // 作业队列按状态计数
  const jobDist = jobs.reduce<Record<string, number>>((acc, j) => {
    const k = (j.job_state || 'UNKNOWN').toUpperCase();
    acc[k] = (acc[k] || 0) + 1;
    return acc;
  }, {});
  const jobDistEntries = Object.entries(jobDist).sort((a, b) => b[1] - a[1]);
  const pendingCount = jobDist['PENDING'] || 0;

  // 最近作业：按 submit_time 降序取前 8
  const recentJobs = [...jobs]
    .sort((a, b) => (b.submit_time || 0) - (a.submit_time || 0))
    .slice(0, 8);

  const statusColor =
    status === 'UP' ? 'var(--accent-emerald,#10b981)' : status === 'DEGRADED' ? 'var(--accent-amber,#f59e0b)' : '#64748b';

  return (
    <div>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.5rem' }}>
        <h2 style={{ margin: 0 }}>集群监控</h2>
        <span
          style={{
            padding: '0.3rem 0.85rem',
            borderRadius: 999,
            fontSize: '0.8rem',
            fontWeight: 700,
            color: '#fff',
            background: statusColor,
            boxShadow: `0 0 10px ${statusColor}`,
          }}
        >
          {status}
        </span>
      </div>

      {error && <Notice color="#f59e0b" bg="rgba(245,158,11,.12)">{error}</Notice>}

      {/* 集群概览 */}
      <h3 style={sectionTitle}>集群概览</h3>
      <div style={gridCols(180)}>
        <Stat label="Slurm 版本" value={release || '—'} />
        <Stat label="集群状态" value={status} color={statusColor} />
        <Stat label="节点总数" value={String(nodes.length)} />
        <Stat label="CPU 总核" value={String(totalCpus)} color="var(--accent-primary,#06b6d4)" />
        <Stat label="GPU 总数" value={String(totalGpus)} color="var(--accent-violet,#A855F7)" />
      </div>

      {/* 节点状态分布 */}
      <h3 style={sectionTitle}>节点状态分布</h3>
      {distEntries.length === 0 ? (
        <Empty>当前无节点数据。</Empty>
      ) : (
        <div style={gridCols(160)}>
          {distEntries.map(([state, count]) => (
            <Chip key={state} color={nodeBucketColor(state)} label={state} value={count} />
          ))}
        </div>
      )}

      {/* 资源占用 */}
      <h3 style={sectionTitle}>资源占用</h3>
      <div style={{ ...gridCols(260), marginBottom: '1.5rem' }}>
        <Gauge label="CPU 占用" alloc={cpuAlloc} tot={totalCpus} pct={cpuPct} unit="核" color="var(--accent-primary,#06b6d4)" />
        <Gauge label="内存占用" alloc={memAlloc} tot={memTot} pct={memPct} unit="MB" color="var(--accent-emerald,#10b981)" />
        {totalGpus > 0 && <Gauge label="GPU 占用" alloc={gpuAlloc} tot={totalGpus} pct={gpuPct} unit="卡" color="var(--accent-violet,#A855F7)" />}
      </div>

      {/* 作业队列健康 */}
      <h3 style={sectionTitle}>作业队列健康</h3>
      {jobDistEntries.length === 0 ? (
        <Empty>当前无作业数据。</Empty>
      ) : (
        <div style={gridCols(160)}>
          <Chip color="var(--accent-amber,#f59e0b)" label="排队深度 (PENDING)" value={pendingCount} />
          {jobDistEntries
            .filter(([k]) => k !== 'PENDING')
            .map(([state, count]) => (
              <Chip key={state} color={jobStateColor(state)} label={state} value={count} />
            ))}
        </div>
      )}

      {/* 分区表 */}
      <h3 style={sectionTitle}>分区</h3>
      <div style={{ ...raisedCard, marginBottom: '1.5rem' }}>
        {parts.length === 0 ? (
          <Empty embedded>无分区数据。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={tableStyle}>
              <thead>
                <tr style={trHeadStyle}>
                  <th style={th}>名称</th>
                  <th style={th}>节点</th>
                  <th style={th}>总 CPU</th>
                  <th style={th}>总节点数</th>
                </tr>
              </thead>
              <tbody>
                {parts.map((p) => (
                  <tr key={p.name} style={trBodyStyle}>
                    <td style={td}>{p.name}</td>
                    <td style={td}>{p.nodes || '—'}</td>
                    <td style={td}>{p.total_cpus ?? '—'}</td>
                    <td style={td}>{p.total_nodes ?? '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* 最近作业 */}
      <h3 style={sectionTitle}>最近作业</h3>
      <div style={raisedCard}>
        {recentJobs.length === 0 ? (
          <Empty embedded>无作业记录。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={tableStyle}>
              <thead>
                <tr style={trHeadStyle}>
                  <th style={th}>作业 ID</th>
                  <th style={th}>名称</th>
                  <th style={th}>状态</th>
                  <th style={th}>提交者</th>
                  <th style={th}>提交时间</th>
                </tr>
              </thead>
              <tbody>
                {recentJobs.map((j) => (
                  <tr key={j.job_id} style={trBodyStyle}>
                    <td style={td}>{j.job_id}</td>
                    <td style={td}>{j.name || '—'}</td>
                    <td style={td}>
                      <span
                        style={{
                          padding: '0.15rem 0.5rem',
                          borderRadius: 6,
                          fontSize: '0.72rem',
                          fontWeight: 700,
                          color: '#fff',
                          background: jobStateColor(j.job_state),
                        }}
                      >
                        {j.job_state}
                      </span>
                    </td>
                    <td style={td}>{j.owner || '—'}</td>
                    <td style={td}>{j.submit_time ? new Date(j.submit_time * 1000).toLocaleString() : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

// --- 样式常量 ---
const raisedCard: CSSProperties = {
  background: 'var(--bg-card,#1b1e28)',
  border: '1px solid var(--border-color,#2a2f3a)',
  borderRadius: 12,
  padding: '1.25rem',
  boxShadow: 'var(--shadow-card)',
  transition: 'box-shadow .3s ease',
};

const sectionTitle: CSSProperties = {
  margin: '0 0 1rem 0',
  fontSize: '1.05rem',
  fontWeight: 700,
  color: 'var(--text-main,#f1f5f9)',
};

const tableStyle: CSSProperties = { width: '100%', borderCollapse: 'collapse', fontSize: '0.88rem' };
const trHeadStyle: CSSProperties = {
  textAlign: 'left',
  color: 'var(--text-muted,#94a3b8)',
  borderBottom: '1px solid var(--border-color,#2a2f3a)',
};
const trBodyStyle: CSSProperties = { borderBottom: '1px solid var(--border-color,#2a2f3a)' };
const th: CSSProperties = { padding: '0.5rem' };
const td: CSSProperties = { padding: '0.5rem' };

function gridCols(min: number): CSSProperties {
  return {
    display: 'grid',
    gridTemplateColumns: `repeat(auto-fit,minmax(${min}px,1fr))`,
    gap: '1rem',
    marginBottom: '1.5rem',
  };
}

// --- 状态分类 / 配色 ---
function nodeBucket(state: string): string {
  const s = (state || '').toUpperCase();
  if (s.includes('DOWN') || s.includes('FAIL')) return 'DOWN';
  if (s.includes('DRAIN')) return 'DRAIN';
  if (s.includes('MIXED')) return 'MIXED';
  if (s.includes('ALLOCATED')) return 'ALLOCATED';
  if (s.includes('IDLE')) return 'IDLE';
  return 'OTHER';
}

function nodeBucketColor(bucket: string): string {
  switch (bucket) {
    case 'IDLE':
      return 'var(--accent-emerald,#10b981)';
    case 'MIXED':
    case 'ALLOCATED':
      return 'var(--accent-primary,#06b6d4)';
    case 'DRAIN':
      return 'var(--accent-amber,#f59e0b)';
    case 'DOWN':
      return 'var(--accent-rose,#f43f5e)';
    default:
      return 'var(--text-muted,#94a3b8)';
  }
}

function jobStateColor(s: string): string {
  const st = (s || '').toUpperCase();
  if (st === 'RUNNING') return 'var(--accent-emerald,#10b981)';
  if (st === 'PENDING' || st === 'HELD' || st === 'CONFIGURING') return 'var(--accent-amber,#f59e0b)';
  if (st === 'COMPLETED') return '#64748b';
  if (st === 'CANCELLED' || st === 'FAILED' || st === 'TIMEOUT' || st === 'OUT_OF_MEMORY') return 'var(--accent-rose,#f43f5e)';
  return 'var(--accent-primary,#06b6d4)';
}

// --- 子组件 ---
function Stat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={raisedCard}>
      <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.8rem', marginBottom: '0.5rem' }}>{label}</div>
      <div style={{ fontSize: '1.5rem', fontWeight: 700, color: color || 'var(--text-main,#f1f5f9)', wordBreak: 'break-all' }}>
        {value}
      </div>
    </div>
  );
}

function Chip({ color, label, value }: { color: string; label: string; value: number }) {
  return (
    <div
      style={{
        ...raisedCard,
        display: 'flex',
        alignItems: 'center',
        gap: '0.6rem',
        padding: '0.85rem 1rem',
      }}
    >
      <span style={{ width: 10, height: 10, borderRadius: 999, background: color, boxShadow: `0 0 8px ${color}`, flexShrink: 0 }} />
      <span style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.78rem', fontWeight: 600 }}>{label}</span>
      <span style={{ marginLeft: 'auto', fontSize: '1.25rem', fontWeight: 700, color: 'var(--text-main,#f1f5f9)' }}>{value}</span>
    </div>
  );
}

function Gauge({
  label,
  alloc,
  tot,
  pct,
  unit,
  color,
}: {
  label: string;
  alloc: number;
  tot: number;
  pct: number;
  unit: string;
  color: string;
}) {
  return (
    <div style={raisedCard}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
        <span style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.85rem' }}>{label}</span>
        <span style={{ fontWeight: 700, color }}>{pct}%</span>
      </div>
      <div
        style={{
          background: 'var(--bg-card-hover,#222632)',
          borderRadius: 6,
          height: 10,
          overflow: 'hidden',
          marginBottom: '0.4rem',
          boxShadow: 'var(--shadow-inset-deep)',
        }}
      >
        <div style={{ width: `${pct}%`, height: '100%', background: color, boxShadow: `0 0 8px ${color}`, transition: 'width .3s' }} />
      </div>
      <div style={{ fontSize: '0.78rem', color: 'var(--text-muted,#888)' }}>
        {alloc} / {tot} {unit} 分配
      </div>
    </div>
  );
}

function Empty({ children, embedded }: { children: ReactNode; embedded?: boolean }) {
  return (
    <div
      style={{
        color: 'var(--text-muted,#888)',
        ...(embedded ? {} : { marginBottom: '1.5rem' }),
      }}
    >
      {children}
    </div>
  );
}

function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return (
    <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>
      {children}
    </div>
  );
}
