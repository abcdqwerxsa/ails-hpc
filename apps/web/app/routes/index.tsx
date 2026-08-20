import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { slurm, type JobSummary, type NodeStateInfo, type Partition } from '../services/slurm';
import { LedBeacon, NeuProgressBar, Notice, resolveLedTone, type LedTone } from '../components/panel_ui';

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
  const gpuAlloc = nodes.reduce((s, n) => s + (n.alloc_gpus || 0), 0);
  const gpuTot = nodes.reduce((s, n) => s + (n.gpus || 0), 0);

  const clusterLedTone: LedTone = status === 'UP' ? 'emerald' : status === 'DEGRADED' ? 'amber' : 'idle';

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.5rem' }}>
        <h2 style={{ margin: 0, display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
          <span>集群总览</span>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: '0.4rem', fontSize: '0.82rem', fontWeight: 600, color: 'var(--text-muted)' }}>
            <LedBeacon tone={clusterLedTone} />
            {status}
          </span>
        </h2>
        {release && (
          <span style={{ fontSize: '0.78rem', color: 'var(--text-dim)', background: 'var(--card-bg)', padding: '0.3rem 0.65rem', borderRadius: 8, boxShadow: 'var(--shadow-inset-deep)' }}>
            Slurm {release}
          </span>
        )}
      </div>

      {error && <Notice color="#f59e0b" bg="rgba(245,158,11,.12)">{error}</Notice>}

      {/* 节点异常告警 */}
      {(() => {
        const badNodes = nodes.filter((n) => {
          const st = (n.state || '').toUpperCase();
          return st.includes('DOWN') || st.includes('DRAIN') || st.includes('FAIL');
        });
        const alerts: string[] = [];
        if (badNodes.length > 0) {
          alerts.push(`节点异常：${badNodes.map((n) => `${n.name}(${n.state})`).join('、')}`);
        }
        if (pending >= 5) {
          alerts.push(`排队堆积：${pending} 个 PENDING 作业`);
        }
        if (alerts.length === 0) return null;
        return (
          <Notice color="#f43f5e" bg="rgba(244,63,94,.12)">
            ⚠ {alerts.join('；')} —— <Link to="/nodes" style={{ color: 'inherit', fontWeight: 700 }}>查看节点</Link> / <Link to="/jobs" style={{ color: 'inherit', fontWeight: 700 }}>查看队列</Link>
          </Notice>
        );
      })()}

      {/* 核心指标硬件卡 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: '1.25rem', marginBottom: '1.75rem' }}>
        <StatCard label="集群状态" value={status} tone={clusterLedTone} />
        <StatCard label="节点总数" value={String(nodes.length)} hint="集群已注册节点" />
        <StatCard label="IDLE / DRAIN" value={`${idle} / ${drained}`} hint="空闲 / 排空节点" />
        <StatCard label="作业总数" value={String(jobs.length)} hint="当前队列所有作业" />
        <StatCard label="RUNNING / PENDING" value={`${running} / ${pending}`} hint="运行中 / 排队中" accentColor="#06b6d4" />
      </div>

      {/* 硬件凹槽进度条：CPU / 内存 / GPU 占用 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(280px,1fr))', gap: '1.25rem', marginBottom: '1.75rem' }}>
        <div className="neu-chiseled-card" style={{ padding: '1.25rem' }}>
          <NeuProgressBar label="CPU 资源利用率" value={cpuAlloc} total={cpuTot} unit="核" color="cyan" />
        </div>
        <div className="neu-chiseled-card" style={{ padding: '1.25rem' }}>
          <NeuProgressBar label="内存资源利用率" value={memAlloc} total={memTot} unit="MB" color="emerald" />
        </div>
        {gpuTot > 0 && (
          <div className="neu-chiseled-card" style={{ padding: '1.25rem' }}>
            <NeuProgressBar label="GPU 资源利用率" value={gpuAlloc} total={gpuTot} unit="卡" color="violet" />
          </div>
        )}
      </div>

      {/* 节点状态矩阵 */}
      <div className="neu-chiseled-card" style={{ padding: '1.35rem', marginBottom: '1.75rem' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1rem' }}>
          <h3 style={{ margin: 0, fontSize: '1rem', fontWeight: 700 }}>节点状态矩阵</h3>
          <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>点击节点可跳转详情</span>
        </div>
        {nodes.length === 0 ? (
          <div style={{ color: 'var(--text-muted)', fontSize: '0.85rem' }}>暂无节点数据</div>
        ) : (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(84px, 1fr))', gap: '0.75rem' }}>
            {nodes.map((n) => {
              const tone = resolveLedTone(n.state);
              return (
                <button
                  key={n.name}
                  type="button"
                  className={`neu-node-chip tone-${tone}`}
                  title={`${n.name} (${n.state}) · 点击进入节点监控与操作`}
                  onClick={() => navigate({ to: '/nodes' })}
                >
                  <LedBeacon tone={tone} />
                  <span className="chip-name">{n.name}</span>
                  <span className="chip-state">{n.state}</span>
                </button>
              );
            })}
          </div>
        )}
      </div>

      {/* 作业队列 */}
      <div className="neu-chiseled-card" style={{ padding: '1.35rem', marginBottom: '1.75rem' }}>
        <h3 style={{ margin: '0 0 1rem', fontSize: '1rem', fontWeight: 700 }}>作业队列状态</h3>
        {jobs.length === 0 ? (
          <div style={{ color: 'var(--text-muted)', fontSize: '0.85rem' }}>暂无作业</div>
        ) : (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.85rem' }}>
            <JobTactileStat label="RUNNING" value={running} tone="emerald" />
            <JobStat label="PENDING" value={pending} tone="amber" />
            <JobStat label="HELD" value={held} tone="amber" />
            <JobStat label="COMPLETED" value={completed} tone="idle" />
            <JobStat label="FAILED" value={failed} tone="rose" />
          </div>
        )}
      </div>

      {/* 分区概览 */}
      <div className="table-card neu-chiseled-card" style={{ marginBottom: '1.75rem' }}>
        <div className="table-header">
          <h3 style={{ margin: 0, fontSize: '1rem' }}>分区概览</h3>
        </div>
        <table className="custom-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>节点清单</th>
              <th>总 CPU</th>
              <th>总节点数</th>
            </tr>
          </thead>
          <tbody>
            {partitions.length === 0 ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--text-muted)', textAlign: 'center', padding: '2rem' }}>暂无分区数据</td>
              </tr>
            ) : (
              partitions.map((p) => (
                <tr key={p.name}>
                  <td style={{ fontWeight: 700, color: 'var(--accent-cyan)' }}>{p.name}</td>
                  <td style={{ color: 'var(--text-muted)', fontFamily: "'JetBrains Mono', monospace" }}>{p.nodes}</td>
                  <td style={{ fontFamily: "'JetBrains Mono', monospace" }}>{p.total_cpus}</td>
                  <td style={{ fontFamily: "'JetBrains Mono', monospace" }}>{p.total_nodes}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function StatCard({ label, value, hint, tone, accentColor }: { label: string; value: string; hint?: string; tone?: LedTone; accentColor?: string }) {
  return (
    <div className="neu-chiseled-card" style={{ padding: '1.25rem', display: 'flex', flexDirection: 'column', gap: '0.4rem' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ color: 'var(--text-muted)', fontSize: '0.75rem', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.04em' }}>{label}</span>
        {tone && <LedBeacon tone={tone} />}
      </div>
      <div style={{ fontSize: '1.65rem', fontWeight: 800, fontFamily: "'JetBrains Mono', monospace", color: accentColor || 'var(--text-main)' }}>
        {value}
      </div>
      {hint && <div style={{ fontSize: '0.72rem', color: 'var(--text-dim)' }}>{hint}</div>}
    </div>
  );
}

function stateTint(state: string): string {
  const s = (state || '').toUpperCase();
  if (s.includes('DRAIN')) return 'rgba(245,158,11,.12)';
  if (s.includes('DOWN') || s.includes('FAIL')) return 'rgba(244,63,94,.12)';
  if (s.includes('ALLOCATED') || s.includes('MIXED')) return 'rgba(6,182,212,.12)';
  return 'rgba(16,185,129,.12)'; // IDLE
}

function JobTactileStat({ label, value, tone }: { label: string; value: number; tone: LedTone }) {
  return (
    <div style={{ background: 'var(--card-bg)', boxShadow: 'var(--shadow-btn)', borderRadius: 10, padding: '0.65rem 1rem', display: 'flex', alignItems: 'center', gap: '0.6rem', minWidth: 130, border: '1px solid var(--border-color)' }}>
      <LedBeacon tone={tone} />
      <span style={{ fontSize: '0.78rem', fontWeight: 600, color: 'var(--text-muted)' }}>{label}</span>
      <strong style={{ marginLeft: 'auto', fontSize: '1.1rem', fontFamily: "'JetBrains Mono', monospace", color: 'var(--text-main)' }}>{value}</strong>
    </div>
  );
}

function JobStat({ label, value, tone }: { label: string; value: number; tone: LedTone }) {
  return (
    <div style={{ background: 'var(--card-bg)', boxShadow: 'var(--shadow-btn)', borderRadius: 10, padding: '0.65rem 1rem', display: 'flex', alignItems: 'center', gap: '0.6rem', minWidth: 130, border: '1px solid var(--border-color)' }}>
      <LedBeacon tone={tone} />
      <span style={{ fontSize: '0.78rem', fontWeight: 600, color: 'var(--text-muted)' }}>{label}</span>
      <strong style={{ marginLeft: 'auto', fontSize: '1.1rem', fontFamily: "'JetBrains Mono', monospace", color: 'var(--text-main)' }}>{value}</strong>
    </div>
  );
}
