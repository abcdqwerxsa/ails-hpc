import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useEffect, useState } from 'react';
import { slurm, type NodeStateInfo, type NodeStateOp } from '../services/slurm';
import { can } from '../services/auth';
import { LedBeacon, NeuProgressBar, StatusBadge, resolveLedTone } from '../components/panel_ui';

export const Route = createFileRoute('/nodes')({ component: NodesPage });

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
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.75rem' }}>
        <div>
          <h2 style={{ margin: 0 }}>节点状态</h2>
          <p style={{ margin: '0.25rem 0 0', fontSize: '0.85rem', color: 'var(--text-muted)' }}>
            实时监控计算节点的硬件资源分配与健康度状态
          </p>
        </div>
        <button className="btn-primary" onClick={refresh} style={{ padding: '0.5rem 1.15rem' }}>
          刷新状态
        </button>
      </div>

      {error && (
        <div style={{ padding: '0.75rem 1.1rem', background: 'rgba(239,68,68,.12)', color: '#f43f5e', borderRadius: 10, marginBottom: '1.25rem', fontSize: '.9rem', border: '1px solid rgba(244,63,94,0.25)', boxShadow: 'var(--shadow-btn)' }}>
          {error}
        </div>
      )}

      {loading ? (
        <div style={{ color: 'var(--text-muted)', padding: '2rem 0' }}>加载中…</div>
      ) : nodes.length === 0 ? (
        <div style={{ color: 'var(--text-muted)', padding: '2rem 0' }}>当前无节点（slurmrestd 可能不可达）。</div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))', gap: '1.25rem' }}>
          {nodes.map((n) => {
            const drained = (n.state || '').toUpperCase().includes('DRAIN');
            return (
              <div
                key={n.name}
                className="neu-chiseled-card"
                style={{
                  padding: '1.35rem',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: '1rem',
                }}
              >
                {/* 节点头部：名称 + 状态指示灯 */}
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <strong style={{ fontSize: '1.1rem', fontFamily: "'JetBrains Mono', monospace" }}>{n.name}</strong>
                  <StatusBadge status={n.state} tone={resolveLedTone(n.state)} />
                </div>

                {/* 资源占用凹槽发光条 */}
                <div style={{ display: 'grid', gap: '0.75rem' }}>
                  <NeuProgressBar label="CPU 分配" value={n.alloc_cpus} total={n.cpus} unit="核" color="cyan" />
                  <NeuProgressBar label="内存分配" value={n.alloc_memory} total={n.real_memory} unit="MB" color="emerald" />
                  {n.gpus > 0 && (
                    <NeuProgressBar label="GPU 分配" value={n.alloc_gpus} total={n.gpus} unit="卡" color="violet" />
                  )}
                </div>

                {/* 附加属性 */}
                <div style={{ fontSize: '0.8rem', color: 'var(--text-dim)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <span>物理核心：{n.cores} Cores</span>
                  {n.reason && <span style={{ color: 'var(--accent-amber)', fontWeight: 600 }}>原因：{n.reason}</span>}
                </div>

                {/* 控制台操作按钮 */}
                {can('nodes:manage') ? (
                  <div style={{ display: 'flex', gap: '0.65rem', marginTop: '0.25rem' }}>
                    <button
                      className="btn-primary"
                      style={{
                        flex: 1,
                        opacity: drained ? 0.45 : 1,
                        padding: '0.45rem 0.75rem',
                        fontSize: '0.8rem',
                        background: drained ? 'var(--card-bg)' : undefined,
                        color: drained ? 'var(--text-muted)' : undefined,
                      }}
                      disabled={!!acting || drained}
                      onClick={() => act(n.name, 'DRAIN')}
                    >
                      {acting === n.name + 'DRAIN' ? '…' : 'DRAIN (排空)'}
                    </button>
                    <button
                      className="neu-btn"
                      style={{
                        flex: 1,
                        opacity: !drained ? 0.45 : 1,
                        padding: '0.45rem 0.75rem',
                        fontSize: '0.8rem',
                        color: !drained ? 'var(--text-muted)' : 'var(--accent-emerald)',
                      }}
                      disabled={!!acting || !drained}
                      onClick={() => act(n.name, 'RESUME')}
                    >
                      {acting === n.name + 'RESUME' ? '…' : 'RESUME (恢复)'}
                    </button>
                  </div>
                ) : (
                  <div style={{ fontSize: '0.75rem', color: 'var(--text-dim)', textAlign: 'center' }}>
                    只读视图（需 nodes:manage 权限操作）
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

