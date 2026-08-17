import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useState, type ReactNode } from 'react';
import { slurm, type HistoryEntry } from '../services/slurm';

export const Route = createFileRoute('/history')({ component: HistoryPage });

function stateColor(s: string): string {
  const st = (s || '').toUpperCase();
  if (st === 'RUNNING') return '#10b981';
  if (st === 'PENDING' || st === 'HELD') return '#f59e0b';
  if (st === 'COMPLETED') return '#64748b';
  if (st === 'CANCELLED' || st === 'FAILED' || st === 'TIMEOUT' || st === 'OUT_OF_MEMORY') return '#f43f5e';
  return '#3b82f6';
}

const STATE_FILTERS = ['全部', 'COMPLETED', 'FAILED', 'RUNNING', 'CANCELLED'];

function HistoryPage() {
  const [rows, setRows] = useState<HistoryEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [state, setState] = useState('全部');

  const load = async (st: string) => {
    setLoading(true);
    setError('');
    try {
      const r = await slurm.getJobHistory(st === '全部' ? undefined : st);
      setRows(r.history || []);
    } catch (e: any) {
      setError(e?.message || '加载历史失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load(state);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1rem' }}>
        <h2 style={{ margin: 0 }}>作业历史</h2>
        <button className="btn-primary" onClick={() => load(state)} style={{ padding: '0.3rem 0.9rem' }}>刷新</button>
      </div>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}

      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', marginBottom: '0.75rem' }}>
        {STATE_FILTERS.map((f) => {
          const active = state === f;
          return (
            <button
              key={f}
              onClick={() => { setState(f); load(f); }}
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
              {f}
            </button>
          );
        })}
      </div>

      {loading ? (
        <div style={{ color: 'var(--text-muted,#94a3b8)' }}>加载中…</div>
      ) : rows.length === 0 ? (
        <div style={{ color: 'var(--text-muted,#94a3b8)' }}>暂无历史作业。</div>
      ) : (
        <div className="table-card">
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.85rem' }}>
              <thead>
                <tr style={{ textAlign: 'left', color: 'var(--text-muted,#94a3b8)', borderBottom: '1px solid var(--border-color,#2a2f3a)' }}>
                  <th style={th}>ID</th>
                  <th style={th}>名称</th>
                  <th style={th}>属主</th>
                  <th style={th}>分区</th>
                  <th style={th}>状态</th>
                  <th style={th}>耗时</th>
                  <th style={th}>ExitCode</th>
                  <th style={th}>提交时间</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((j, i) => (
                  <tr key={j.job_id} style={{ borderBottom: i === rows.length - 1 ? 'none' : '1px solid var(--row-line,#2a2f3a)' }}>
                    <td style={{ ...td, ...mono }}>{j.job_id}</td>
                    <td style={td}>{j.name || '-'}</td>
                    <td style={{ ...td, ...mono, fontSize: '0.78rem' }}>{j.owner || '-'}</td>
                    <td style={td}>{j.partition || '-'}</td>
                    <td style={td}>
                      <span style={{ padding: '0.15rem 0.5rem', borderRadius: 6, fontSize: '0.72rem', fontWeight: 700, color: '#fff', background: stateColor(j.state) }}>
                        {j.state}
                      </span>
                    </td>
                    <td style={{ ...td, ...mono }}>{j.elapsed_sec != null ? fmtElapsed(j.elapsed_sec) : '-'}</td>
                    <td style={{ ...td, ...mono }}>{j.exit_code || '-'}</td>
                    <td style={{ ...td, ...mono, fontSize: '0.72rem' }}>{(j.submit || '').replace('T', ' ').slice(0, 19) || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

const fmtElapsed = (s: number) => (s >= 60 ? `${Math.floor(s / 60)}m${s % 60}s` : `${s}s`);

const th = { padding: '0.7rem 1rem', fontSize: '0.72rem', textTransform: 'uppercase' as const, letterSpacing: '0.05em', fontWeight: 700 };
const td = { padding: '0.7rem 1rem' };
const mono = { fontFamily: "'JetBrains Mono', monospace" };

function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{children}</div>;
}
