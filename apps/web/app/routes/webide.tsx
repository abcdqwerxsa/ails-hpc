import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useEffect, useState, type ChangeEvent, type ReactNode } from 'react';
import { can } from '../services/auth';
import { slurm, ideFullURL, type ContainerInstance } from '../services/slurm';

export const Route = createFileRoute('/webide')({ component: WebIDEPage });

function WebIDEPage() {
  const [sessions, setSessions] = useState<ContainerInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [envType, setEnvType] = useState<'jupyter' | 'vscode'>('jupyter');
  const [cpus, setCpus] = useState('2');
  const [memMb, setMemMb] = useState('4096');
  const [durationMin, setDurationMin] = useState('120');
  const [launching, setLaunching] = useState(false);
  const [acting, setActing] = useState('');

  const refresh = useCallback(async () => {
    try {
      const r = await slurm.listContainers();
      setSessions(r.containers || []);
      setError('');
    } catch (e: any) {
      setError(e?.message || '加载会话失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, [refresh]);

  const launch = async () => {
    setLaunching(true);
    setError('');
    setInfo('');
    try {
      const r = await slurm.launchContainer({
        env_type: envType,
        cpus: Number(cpus) || 2,
        memory_mb: Number(memMb) || 4096,
        time_limit_min: Number(durationMin) || 120,
      });
      setInfo(`已启动 ${envType} 会话（作业 #${r.allocated?.job_id ?? '-'}）。等状态变 RUNNING 后点"打开 IDE"。`);
      await refresh();
    } catch (e: any) {
      setError(`启动失败：${e?.message || e}`);
    } finally {
      setLaunching(false);
    }
  };

  const extend = async (id: string) => {
    const v = prompt('延长会话多少分钟？（1-720）', '60');
    const m = Number(v);
    if (!v || !m || m < 1 || m > 720) return;
    setActing(id + ':extend');
    setError(''); setInfo('');
    try {
      await slurm.extendSession(id, m);
      setInfo(`会话已延长 ${m} 分钟`);
    } catch (e: any) {
      setError(`续期失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  const recycle = async (id: string) => {
    setActing(id);
    setError('');
    setInfo('');
    try {
      const r = await slurm.recycleContainer(id);
      setInfo(r.message);
      await refresh();
    } catch (e: any) {
      setError(`回收失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1rem' }}>Web-IDE（交互式开发环境）</h2>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      <div
        style={{
          background: 'var(--bg-card,#1b1e28)',
          border: '1px solid var(--border-color,#2a2f3a)',
          borderRadius: 12,
          padding: '1.25rem',
          marginBottom: '1.5rem',
          display: 'flex',
          gap: '0.75rem',
          alignItems: 'end',
          flexWrap: 'wrap',
          boxShadow: 'var(--shadow-card)',
          transition: 'box-shadow .3s ease',
        }}
      >
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          环境
          <select
            className="form-control"
            value={envType}
            onChange={(e: ChangeEvent<HTMLSelectElement>) => setEnvType(e.target.value as 'jupyter' | 'vscode')}
          >
            <option value="jupyter">JupyterLab</option>
            <option value="vscode">VS Code (code-server)</option>
          </select>
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          CPU
          <input className="form-control" type="number" min="1" max="8" value={cpus}
            onChange={(e) => setCpus(e.target.value)} style={{ width: 90 }} />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          内存 MB
          <input className="form-control" type="number" min="512" max="5950" step="256" value={memMb}
            onChange={(e) => setMemMb(e.target.value)} style={{ width: 110 }} />
        </label>
        <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
          时长（分钟）
          <select className="form-control" value={durationMin}
            onChange={(e) => setDurationMin(e.target.value)} style={{ width: 110 }}>
            <option value="30">30</option>
            <option value="60">60</option>
            <option value="120">120（默认）</option>
            <option value="240">240</option>
            <option value="480">480</option>
            <option value="720">720（上限）</option>
          </select>
        </label>
        {can('ide:manage') && (
          <button className="btn-primary" onClick={launch} disabled={launching} style={{ padding: '0.5rem 1.5rem' }}>
            {launching ? '启动中…' : '启动 IDE 会话'}
          </button>
        )}
      </div>

      <h3 style={{ margin: '0 0 0.75rem', fontSize: '1.05rem' }}>活跃会话</h3>
      {loading ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>加载中…</div>
      ) : sessions.length === 0 ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>当前无活跃会话，点上方"启动"创建。</div>
      ) : (
        <div style={{ display: 'grid', gap: '0.75rem' }}>
          {sessions.map((s) => {
            const running = s.status === 'RUNNING';
            return (
              <div
                key={s.container_id}
                style={{
                  background: 'var(--bg-card,#1b1e28)',
                  border: '1px solid var(--border-color,#2a2f3a)',
                  borderRadius: 10,
                  padding: '1rem',
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  gap: '1rem',
                  flexWrap: 'wrap',
                  boxShadow: 'var(--shadow-card)',
                  transition: 'box-shadow .3s ease',
                }}
              >
                <div style={{ display: 'grid', gap: '0.2rem', fontSize: '0.88rem' }}>
                  <div>
                    <strong>{s.env_type === 'vscode' ? 'VS Code' : 'JupyterLab'}</strong> · 作业 #{s.job_id} · {s.node || '...'}
                  </div>
                  <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.8rem' }}>
                    CPU {s.cpus} · 内存 {s.memory_mb}MB · 节点 {s.nodes}
                  </div>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
                  <span style={{ padding: '0.2rem 0.6rem', borderRadius: 6, fontSize: '0.72rem', fontWeight: 700, color: '#fff', background: running ? '#10b981' : '#f59e0b' }}>
                    {s.status}
                  </span>
                  {can('ide:manage') && (
                    <button
                      className="btn-primary"
                      disabled={!running}
                      onClick={() => window.open(ideFullURL(s.web_url), '_blank')}
                      style={{ padding: '0.35rem 1rem' }}
                    >
                      打开 IDE
                    </button>
                  )}
                  {can('ide:manage') && (
                    <>
                      <button
                        className="neu-btn"
                        onClick={() => extend(s.container_id)}
                        disabled={!!acting || !running}
                      >
                        续期
                      </button>
                      <button
                        onClick={() => recycle(s.container_id)}
                        disabled={!!acting}
                        style={{
                          padding: '0.4rem 0.9rem',
                          borderRadius: 8,
                          border: 'none',
                          background: 'var(--card-bg)',
                          boxShadow: 'var(--shadow-btn)',
                          color: 'var(--accent-rose)',
                          cursor: 'pointer',
                          transition: 'box-shadow .2s ease',
                        }}
                      >
                        回收
                      </button>
                    </>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
      <div style={{ marginTop: '1rem', fontSize: '0.78rem', color: 'var(--text-muted,#888)' }}>
        提示：启动后约 10–30s 进入 RUNNING；VS Code 与 Jupyter 均支持，点"打开 IDE"在新标签页打开。
      </div>
    </div>
  );
}

function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{children}</div>;
}
