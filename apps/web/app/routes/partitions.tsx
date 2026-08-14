import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useState, type ReactNode } from 'react';
import { slurm, type Partition } from '../services/slurm';

export const Route = createFileRoute('/partitions')({ component: PartitionsPage });

function PartitionsPage() {
  const [parts, setParts] = useState<Partition[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    (async () => {
      try {
        const r = await slurm.getPartitions();
        setParts(r.partitions || []);
        setError('');
      } catch (e: any) {
        setError(e?.message || '加载分区失败');
      } finally {
        setLoading(false);
      }
    })();
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

      <h2 style={{ marginTop: 0, marginBottom: '1.5rem' }}>分区</h2>

      {error && <Notice color="#f43f5e" bg="rgba(244,63,94,.12)">{error}</Notice>}

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
                  <th style={th}>总节点数</th>
                  <th style={th}>集群 CPU 占比</th>
                </tr>
              </thead>
              <tbody>
                {parts.map((p, i) => {
                  const pct = totalCpus > 0 ? Math.round(((p.total_cpus || 0) / totalCpus) * 100) : 0;
                  const isLast = i === parts.length - 1;
                  return (
                    <tr
                      key={p.name}
                      className="pt-row"
                      style={{
                        borderBottom: isLast ? 'none' : '1px solid var(--border-color,#2a2f3a)',
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
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
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

function Stat({ label, value }: { label: string; value: string }) {
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
          color: 'var(--text-main,#f1f5f9)',
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
