import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { slurm, type NodeStateInfo } from '../services/slurm';

export const Route = createFileRoute('/')({ component: OverviewPage });

function OverviewPage() {
  const [status, setStatus] = useState<'UP' | 'DEGRADED' | '…'>('…');
  const [release, setRelease] = useState('');
  const [nodes, setNodes] = useState<NodeStateInfo[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    const tick = async () => {
      // 集群状态：ping 成功且 pings[0].ping==="UP" → UP；否则（含 503）DEGRADED
      try {
        const p = await slurm.getClusterStatus();
        const up = (p.pings?.[0]?.ping || '').toUpperCase() === 'UP';
        setStatus(up ? 'UP' : 'DEGRADED');
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
        /* 节点读取失败已由 status 反映 */
      }
    };
    tick();
    const t = setInterval(tick, 5000);
    return () => clearInterval(t);
  }, []);

  const idle = nodes.filter((n) => (n.state || '').toUpperCase() === 'IDLE').length;
  const drained = nodes.filter((n) => (n.state || '').toUpperCase().includes('DRAIN')).length;

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1.5rem' }}>集群总览</h2>

      {error && (
        <div style={{ padding: '0.65rem 0.9rem', background: 'rgba(239,68,68,.1)', color: '#f43f5e', borderRadius: 8, marginBottom: '1rem', fontSize: '0.9rem' }}>
          {error}
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: '1rem' }}>
        <StatCard label="集群状态" value={status} color={status === 'UP' ? '#10b981' : status === 'DEGRADED' ? '#f59e0b' : '#888'} />
        <StatCard label="节点总数" value={String(nodes.length)} />
        <StatCard label="IDLE" value={String(idle)} color="#10b981" />
        <StatCard label="DRAIN" value={String(drained)} color="#f59e0b" />
      </div>

      {release && (
        <div style={{ marginTop: '1.5rem', color: 'var(--text-muted, #94a3b8)', fontSize: '0.85rem' }}>
          Slurm 版本：{release}
        </div>
      )}
      <div style={{ marginTop: '0.5rem', color: 'var(--text-muted, #94a3b8)', fontSize: '0.8rem' }}>
        提示：节点详情与 DRAIN/RESUME 操作见侧栏「节点状态」。
      </div>
    </div>
  );
}

function StatCard({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ background: 'var(--bg-card, #1b1e28)', border: '1px solid var(--border-color, #2a2f3a)', borderRadius: 12, padding: '1.25rem' }}>
      <div style={{ color: 'var(--text-muted, #94a3b8)', fontSize: '0.8rem', marginBottom: '0.5rem' }}>{label}</div>
      <div style={{ fontSize: '1.6rem', fontWeight: 700, color: color || 'var(--text-main, #f1f5f9)' }}>{value}</div>
    </div>
  );
}
