import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useEffect, useState } from 'react';
import { slurm, type NodeStateInfo, type NodeStateOp } from '../services/slurm';
import { can } from '../services/auth';

export const Route = createFileRoute('/nodes')({ component: NodesPage });

function stateColor(state: string): string {
  const s = (state || '').toUpperCase();
  if (s.includes('DRAIN')) return '#f59e0b';
  if (s.includes('DOWN') || s.includes('FAIL')) return '#f43f5e';
  if (s.includes('ALLOCATED') || s.includes('MIXED')) return '#3b82f6';
  return '#10b981'; // IDLE
}

function NodesPage() {
  const [nodes, setNodes] = useState<NodeStateInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [acting, setActing] = useState('');

  const refresh = useCallback(async () => {
    try {
      const r = await slurm.getNodes();
      setNodes(r.nodes || []);
      setError('');
    } catch (e: any) {
      setError(e?.message || '加载节点失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, [refresh]);

  const act = async (name: string, state: NodeStateOp) => {
    setActing(name + state);
    try {
      await slurm.updateNodeState(name, state, state === 'DRAIN' ? 'API drain' : undefined);
      await refresh();
      setError('');
    } catch (e: any) {
      setError(`${name} ${state} 失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.5rem' }}>
        <h2 style={{ margin: 0 }}>节点状态</h2>
        <button className="btn-primary" onClick={refresh} style={{ padding: '0.4rem 1rem' }}>刷新</button>
      </div>

      {error && (
        <div style={{ padding: '0.65rem 0.9rem', background: 'rgba(239,68,68,.1)', color: '#f43f5e', borderRadius: 8, marginBottom: '1rem', fontSize: '.9rem' }}>
          {error}
        </div>
      )}

      {loading ? (
        <div style={{ color: 'var(--text-muted, #888)' }}>加载中…</div>
      ) : nodes.length === 0 ? (
        <div style={{ color: 'var(--text-muted, #888)' }}>当前无节点（slurmrestd 可能不可达）。</div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: '1rem' }}>
          {nodes.map((n) => {
            const drained = (n.state || '').toUpperCase().includes('DRAIN');
            return (
              <div key={n.name} style={{ background: 'var(--bg-card, #1b1e28)', border: '1px solid var(--border-color, #2a2f3a)', borderRadius: 12, padding: '1.25rem', boxShadow: 'var(--shadow-card)', transition: 'box-shadow .3s ease' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.75rem' }}>
                  <strong style={{ fontSize: '1.05rem' }}>{n.name}</strong>
                  <span style={{ padding: '0.2rem 0.6rem', borderRadius: 6, fontSize: '0.72rem', fontWeight: 700, color: '#fff', background: stateColor(n.state) }}>{n.state}</span>
                </div>
                <div style={{ fontSize: '0.85rem', color: 'var(--text-muted, #94a3b8)', display: 'grid', gap: '0.25rem', marginBottom: '1rem' }}>
                  <div>CPU：{n.alloc_cpus}/{n.cpus} 核分配</div>
                  <div>内存：{n.alloc_memory}/{n.real_memory} MB 分配</div>
                  <div>核数：{n.cores}</div>
                  {n.gpus > 0 ? (
                    <div style={{ color: '#a855f7' }}>GPU：{n.alloc_gpus}/{n.gpus} 卡分配</div>
                  ) : (
                    <div>&nbsp;</div>
                  )}
                  {n.reason && <div style={{ color: '#f59e0b' }}>原因：{n.reason}</div>}
                </div>
                {can('nodes:manage') ? (
                  <div style={{ display: 'flex', gap: '0.5rem' }}>
                    <button className="btn-primary" style={{ flex: 1, opacity: drained ? 0.5 : 1 }} disabled={!!acting || drained} onClick={() => act(n.name, 'DRAIN')}>
                      {acting === n.name + 'DRAIN' ? '…' : 'DRAIN'}
                    </button>
                    <button className="neu-btn" style={{ flex: 1, opacity: !drained ? 0.5 : 1 }} disabled={!!acting || !drained} onClick={() => act(n.name, 'RESUME')}>
                      {acting === n.name + 'RESUME' ? '…' : 'RESUME'}
                    </button>
                  </div>
                ) : (
                  <div style={{ fontSize: '0.75rem', color: 'var(--text-muted, #94a3b8)' }}>只读视图（需 nodes:manage 权限控制节点）</div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
