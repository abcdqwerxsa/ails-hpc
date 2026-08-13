import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
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

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1rem' }}>分区</h2>
      {error && (
        <div style={{ padding: '0.6rem 0.9rem', color: '#f43f5e', background: 'rgba(239,68,68,.1)', borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>
          {error}
        </div>
      )}
      {loading ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>加载中…</div>
      ) : parts.length === 0 ? (
        <div style={{ color: 'var(--text-muted,#888)' }}>无分区。</div>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
            <thead>
              <tr style={{ textAlign: 'left', color: 'var(--text-muted,#94a3b8)', borderBottom: '1px solid var(--border-color,#2a2f3a)' }}>
                <th style={th}>名称</th>
                <th style={th}>节点</th>
                <th style={th}>总 CPU</th>
                <th style={th}>总节点数</th>
              </tr>
            </thead>
            <tbody>
              {parts.map((p) => (
                <tr key={p.name} style={{ borderBottom: '1px solid var(--border-color,#2a2f3a)' }}>
                  <td style={td}>{p.name}</td>
                  <td style={td}>{p.nodes || '-'}</td>
                  <td style={td}>{p.total_cpus}</td>
                  <td style={td}>{p.total_nodes}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

const th = { padding: '0.5rem' } as const;
const td = { padding: '0.5rem' } as const;
