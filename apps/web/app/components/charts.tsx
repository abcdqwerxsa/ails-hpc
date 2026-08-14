// 内联 SVG 图表组件（无第三方依赖），供监控页使用。
// 配色由调用方按门户 accent 传入：CPU=cyan / 内存=emerald / GPU=violet / 磁盘=amber。

export interface LineSeries {
  label: string;
  color: string;
  data: number[]; // 0-100 的百分比序列（旧→新）
}

// Donut 单值环形图：显示当前分配百分比。
export function Donut({ pct, color, label, sub }: { pct: number; color: string; label: string; sub?: string }) {
  const size = 132;
  const r = 46;
  const c = 2 * Math.PI * r;
  const v = Math.max(0, Math.min(100, Math.round(pct)));
  const dash = (v / 100) * c;
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '0.45rem' }}>
      <div style={{ position: 'relative', width: size, height: size }}>
        <svg width={size} height={size} viewBox="0 0 100 100" role="img" aria-label={`${label} ${v}%`} style={{ transform: 'rotate(-90deg)' }}>
          <circle cx="50" cy="50" r={r} fill="none" stroke="var(--border-color,#2a2f3a)" strokeWidth="9" />
          {v > 0 && (
            <circle
              cx="50"
              cy="50"
              r={r}
              fill="none"
              strokeWidth="9"
              strokeLinecap="round"
              strokeDasharray={`${dash} ${c - dash}`}
              // 颜色走 style 而非 stroke 属性：color 可为 CSS var()（var 在展示属性中无效）。
              // drop-shadow 内联 var() 同样合法；不追加 66 透明度后缀（对 var() 非法）。
              style={{
                stroke: color,
                transition: 'stroke-dasharray .4s ease',
                filter: `drop-shadow(0 0 4px ${color})`,
              }}
            />
          )}
        </svg>
        <div
          style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: '1.5rem',
            fontWeight: 800,
            color: 'var(--text-main,#f1f5f9)',
            fontFamily: "'JetBrains Mono', monospace",
          }}
        >
          {v}%
        </div>
      </div>
      <div style={{ textAlign: 'center' }}>
        <div style={{ fontSize: '0.8rem', fontWeight: 700, color: 'var(--text-main,#f1f5f9)' }}>{label}</div>
        {sub && <div style={{ fontSize: '0.7rem', color: 'var(--text-muted,#94a3b8)', fontFamily: "'JetBrains Mono', monospace" }}>{sub}</div>}
      </div>
    </div>
  );
}

// LineChart 多序列折线图：序列为 0-100 百分比，旧→新。
export function LineChart({ series, height = 200 }: { series: LineSeries[]; height?: number }) {
  const w = 640;
  const h = height;
  const padL = 28;
  const padR = 12;
  const padT = 10;
  const padB = 18;
  const maxPts = series.reduce((m, s) => Math.max(m, s.data.length), 1);
  const x = (i: number, n: number) => (n <= 1 ? padL : padL + (i / (n - 1)) * (w - padL - padR));
  const y = (v: number) => padT + (1 - Math.max(0, Math.min(100, v)) / 100) * (h - padT - padB);
  const grid = [0, 25, 50, 75, 100];
  const empty = maxPts < 2; // 至少 2 个采样点才能画出线段
  return (
    <svg width="100%" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" role="img" aria-label="资源分配趋势图" style={{ display: 'block' }}>
      {grid.map((g) => (
        <g key={g}>
          <line x1={padL} y1={y(g)} x2={w - padR} y2={y(g)} stroke="var(--border-color,#2a2f3a)" strokeWidth="1" strokeDasharray="3 5" />
          <text x={4} y={y(g) + 4} fontSize="10" fill="var(--text-muted,#94a3b8)" fontFamily="'JetBrains Mono', monospace">
            {g}
          </text>
        </g>
      ))}
      {!empty &&
        series.map((s) =>
          s.data.length > 1 ? (
            <polyline
              key={s.label}
              fill="none"
              strokeWidth="2.5"
              strokeLinejoin="round"
              strokeLinecap="round"
              points={s.data.map((v, i) => `${x(i, s.data.length)},${y(v)}`).join(' ')}
              // stroke 走 style 以支持 CSS var() 主题色
              style={{ stroke: s.color }}
            />
          ) : null,
        )}
      {empty && (
        <text x={w / 2} y={h / 2} textAnchor="middle" fontSize="12" fill="var(--text-muted,#94a3b8)">
          采样中…
        </text>
      )}
    </svg>
  );
}

// Legend 图例（颜色点 + 标签）。
export function ChartLegend({ series }: { series: LineSeries[] }) {
  return (
    <div style={{ display: 'flex', gap: '1rem', flexWrap: 'wrap', justifyContent: 'center', marginTop: '0.5rem' }}>
      {series.map((s) => (
        <span key={s.label} style={{ display: 'inline-flex', alignItems: 'center', gap: '0.35rem', fontSize: '0.78rem', color: 'var(--text-muted,#94a3b8)' }}>
          <span style={{ width: 10, height: 10, borderRadius: 3, background: s.color, display: 'inline-block' }} />
          {s.label}
        </span>
      ))}
    </div>
  );
}
