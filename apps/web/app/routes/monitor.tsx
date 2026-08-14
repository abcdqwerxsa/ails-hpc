import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { slurm, type MonitorSnapshot } from '../services/slurm';
import { ChartLegend, Donut, LineChart, type LineSeries } from '../components/charts';

export const Route = createFileRoute('/monitor')({ component: MonitorPage });

// 滚动窗口点数（5s × 60 = 近 5 分钟趋势）；客户端累积 + 服务端历史播种。
const WIN = 60;
// 门户 accent 配色（CPU=青 / 内存=绿 / GPU=紫 / 磁盘=橙）——引用 CSS 令牌以跟随明暗主题。
const COL = {
  cpu: 'var(--accent-primary,#06b6d4)',
  mem: 'var(--accent-emerald,#10b981)',
  gpu: 'var(--accent-violet,#A855F7)',
  disk: 'var(--accent-amber,#F59E0B)',
};

const pct = (a: number, t: number) => (t > 0 ? Math.min(100, Math.round((a / t) * 100)) : 0);
const gb = (kb: number) => (kb / 1024 / 1024).toFixed(1);
const cap = (arr: number[], v: number) => {
  const n = [...arr, v];
  return n.length > WIN ? n.slice(n.length - WIN) : n;
};

interface Hist {
  cpu: number[];
  mem: number[];
  gpu: number[];
  disk: number[];
}
const emptyHist = (): Hist => ({ cpu: [], mem: [], gpu: [], disk: [] });

function MonitorPage() {
  const [snap, setSnap] = useState<MonitorSnapshot | null>(null);
  const [hist, setHist] = useState<Hist>(emptyHist);
  const [err, setErr] = useState('');
  const [status, setStatus] = useState<'UP' | 'DEGRADED' | '…'>('…');
  const seeded = useRef(false);

  // 挂载时播种服务端持久化趋势（刷新后不丢近 5 分钟）；端点未就绪/失败则静默降级为空启动。
  useEffect(() => {
    slurm
      .getMonitorHistory()
      .then((h) => {
        if (seeded.current) return;
        seeded.current = true;
        const last = (a?: number[]) => (a || []).slice(-WIN);
        setHist({ cpu: last(h.cpu), mem: last(h.mem), gpu: last(h.gpu), disk: last(h.disk) });
      })
      .catch(() => {
        /* 后端未部署 /slurm/monitor/history：保持现状（可能已有实时采样点） */
      });
  }, []);

  useEffect(() => {
    const tick = async () => {
      try {
        const [s, p] = await Promise.all([
          slurm.getMonitorSnapshot(),
          slurm.getClusterStatus().catch(() => null),
        ]);
        setSnap(s);
        setErr('');
        setStatus((p?.pings?.[0]?.ping || '').toUpperCase() === 'UP' ? 'UP' : 'DEGRADED');
        setHist((prev) => ({
          cpu: cap(prev.cpu, pct(s.cpu.alloc, s.cpu.total)),
          mem: cap(prev.mem, pct(s.mem.alloc, s.mem.total)),
          gpu: cap(prev.gpu, pct(s.gpu.alloc, s.gpu.total)),
          disk: cap(prev.disk, s.disk.percent),
        }));
      } catch (e: any) {
        setErr(e?.message || '监控数据加载失败');
        setStatus('DEGRADED');
      }
    };
    tick();
    const t = setInterval(tick, 5000);
    return () => clearInterval(t);
  }, []);

  const hasGpu = (snap?.gpu.total ?? 0) > 0;
  const series: LineSeries[] = [
    { label: 'CPU', color: COL.cpu, data: hist.cpu },
    { label: '内存', color: COL.mem, data: hist.mem },
    { label: '磁盘', color: COL.disk, data: hist.disk },
    ...(hasGpu ? [{ label: 'GPU', color: COL.gpu, data: hist.gpu }] : []),
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.5rem' }}>
        <h2 style={{ margin: 0 }}>集群监控</h2>
        <span
          style={{
            padding: '0.3rem 0.85rem',
            borderRadius: 8,
            fontSize: '0.78rem',
            fontWeight: 700,
            color: status === 'UP' ? 'var(--accent-emerald,#10b981)' : 'var(--accent-amber,#f59e0b)',
            background: 'var(--card-bg)',
            boxShadow: 'var(--shadow-inset-deep)',
          }}
        >
          {status === 'UP' ? '● 集群正常' : status === 'DEGRADED' ? '● 降级' : '● 采样中'}
        </span>
      </div>

      {err && (
        <div style={{ padding: '0.6rem 0.9rem', color: '#f43f5e', background: 'rgba(239,68,68,.1)', borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>
          {err}
        </div>
      )}

      <Section title="资源分配趋势（近 5 分钟）">
        <LineChart series={series} />
        <ChartLegend series={series} />
      </Section>

      <Section title="当前分配率">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(150px,1fr))', gap: '1.5rem', justifyItems: 'center' }}>
          <Donut pct={pct(snap?.cpu.alloc ?? 0, snap?.cpu.total ?? 0)} color={COL.cpu} label="CPU 核" sub={`${snap?.cpu.alloc ?? 0} / ${snap?.cpu.total ?? 0}`} />
          <Donut pct={pct(snap?.mem.alloc ?? 0, snap?.mem.total ?? 0)} color={COL.mem} label="内存" sub={`${snap?.mem.alloc ?? 0} / ${snap?.mem.total ?? 0} MB`} />
          {(snap?.disk.percent ?? -1) < 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: 132, color: 'var(--text-muted,#94a3b8)', fontSize: '0.8rem', textAlign: 'center' }}>
              磁盘 /shared<br />
              用量不可用
            </div>
          ) : (
            <Donut pct={snap!.disk.percent} color={COL.disk} label="磁盘 /shared" sub={`${gb(snap!.disk.used_kb)} / ${gb(snap!.disk.total_kb)} GB`} />
          )}
          {hasGpu && <Donut pct={pct(snap!.gpu.alloc, snap!.gpu.total)} color={COL.gpu} label="GPU" sub={`${snap!.gpu.alloc} / ${snap!.gpu.total} 卡`} />}
        </div>
      </Section>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div
      style={{
        background: 'var(--bg-card,#1b1e28)',
        border: '1px solid var(--border-color,#2a2f3a)',
        borderRadius: 14,
        padding: '1.25rem',
        marginBottom: '1.25rem',
        boxShadow: 'var(--shadow-card)',
      }}
    >
      <h3 style={{ margin: '0 0 1rem', fontSize: '1rem' }}>{title}</h3>
      {children}
    </div>
  );
}
