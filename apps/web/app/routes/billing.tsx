import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useState, type ChangeEvent, type ReactNode } from 'react';
import { slurm, type BillingUsage } from '../services/slurm';

export const Route = createFileRoute('/billing')({ component: BillingPage });

function BillingPage() {
  const [user, setUser] = useState('');
  const [project, setProject] = useState('');
  const [data, setData] = useState<BillingUsage | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');

  const load = async (u?: string, p?: string) => {
    setLoading(true);
    setError('');
    try {
      const r = await slurm.getBillingUsage(u, p);
      setData(r);
    } catch (e: any) {
      setError(e?.message || '读取计费失败');
      setData(null);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const exportJSON = async () => {
    setError('');
    setInfo('');
    try {
      const r = await slurm.exportBillingJSON(user.trim() || undefined, project.trim() || undefined);
      const blob = new Blob([JSON.stringify(r, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `billing-${r.user || 'all'}.json`;
      a.click();
      URL.revokeObjectURL(url);
      setInfo('已导出 JSON 报告');
    } catch (e: any) {
      setError(`导出失败：${e?.message || e}`);
    }
  };

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1rem' }}>计费与资源用量</h2>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      <div style={{ display: 'flex', gap: '0.75rem', alignItems: 'end', flexWrap: 'wrap', marginBottom: '1.5rem' }}>
        <Field label="用户（可选）">
          <input className="form-control" value={user} onChange={(e: ChangeEvent<HTMLInputElement>) => setUser(e.target.value)} placeholder="hpcuser" />
        </Field>
        <Field label="项目（可选）">
          <input className="form-control" value={project} onChange={(e: ChangeEvent<HTMLInputElement>) => setProject(e.target.value)} placeholder="ails-hpc" />
        </Field>
        <button className="btn-primary" onClick={() => load(user.trim() || undefined, project.trim() || undefined)} disabled={loading} style={{ padding: '0.5rem 1.25rem' }}>
          {loading ? '查询中…' : '查询用量'}
        </button>
        <button
          onClick={exportJSON}
          style={{ padding: '0.5rem 1.25rem', borderRadius: 8, border: '1px solid var(--border-color,#2a2f3a)', background: 'var(--bg-card-hover,#222632)', color: 'var(--text-main,#f1f5f9)', cursor: 'pointer' }}
        >
          导出 JSON
        </button>
      </div>

      {data && (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '1rem' }}>
            <Stat label="CPU 小时" value={data.total_cpu_hours.toFixed(2)} color="#3b82f6" />
            <Stat label="内存 GB·小时" value={data.total_memory_gb_hours.toFixed(2)} color="#10b981" />
            <Stat label="GPU 小时" value={data.total_gpu_hours.toFixed(2)} color="#f59e0b" />
            <Stat label="作业数" value={String(data.job_count)} />
            <Stat label="容器会话数" value={String(data.container_count)} />
          </div>
          <div style={{ marginTop: '1rem', fontSize: '0.85rem', color: 'var(--text-muted,#94a3b8)' }}>
            范围：用户 {data.user || '(全部)'}{data.project ? ` · 项目 ${data.project}` : ''}
          </div>
        </>
      )}
    </div>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>{label}{children}</label>;
}
function Stat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ background: 'var(--bg-card,#1b1e28)', border: '1px solid var(--border-color,#2a2f3a)', borderRadius: 12, padding: '1.25rem' }}>
      <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.8rem', marginBottom: '0.5rem' }}>{label}</div>
      <div style={{ fontSize: '1.5rem', fontWeight: 700, color: color || 'var(--text-main,#f1f5f9)' }}>{value}</div>
    </div>
  );
}
function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{children}</div>;
}
