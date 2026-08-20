// 管理类面板共享的小组件与样式常量（自 admin.tsx 抽出——2026-08-19 IA 重组：
// 用户管理/RBAC 管理/调度管理三页共用，定义单一来源）。
import type { ReactNode } from 'react';

export const cardStyle = {
  background: 'var(--bg-card,#1b1e28)',
  borderTop: '1px solid var(--chiseled-top,rgba(255,255,255,0.12))',
  borderBottom: '1px solid var(--chiseled-bottom,rgba(0,0,0,0.15))',
  borderLeft: '1px solid var(--chiseled-left,var(--border-color,#2a2f3a))',
  borderRight: '1px solid var(--chiseled-right,var(--border-color,#2a2f3a))',
  borderRadius: 14,
  padding: '1.35rem',
  display: 'grid',
  gap: '0.85rem',
  boxShadow: 'var(--shadow-card)',
  transition: 'box-shadow .25s ease, transform .2s ease',
} as const;

export const emptyStyle = { padding: '2.5rem', textAlign: 'center', color: 'var(--text-muted,#94a3b8)' } as const;

export const th = {
  padding: '0.85rem 1.25rem',
  fontSize: '0.72rem',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  color: 'var(--text-muted,#94a3b8)',
  fontWeight: 700,
  borderBottom: '1px solid var(--border-color,#2a2f3a)',
} as const;

export const td = {
  padding: '0.9rem 1.25rem',
  fontSize: '0.875rem',
  color: 'var(--text-main,#f1f5f9)',
} as const;

export const mono = { fontFamily: "'JetBrains Mono', monospace" } as const;
export const num = { ...mono, textAlign: 'right' } as const;

// 状态映射色系：active/running/up=emerald / suspended/pending/drain=amber / disabled/failed/down=rose / completed=idle
export type LedTone = 'emerald' | 'amber' | 'rose' | 'cyan' | 'idle';

export function resolveLedTone(status: string): LedTone {
  const s = (status || '').toUpperCase();
  if (['ACTIVE', 'RUNNING', 'UP', 'IDLE', 'OK', 'HEALTHY'].includes(s)) return 'emerald';
  if (['SUSPENDED', 'PENDING', 'HELD', 'CONFIGURING', 'DRAIN', 'DRAINED', 'DEGRADED'].some((k) => s.includes(k))) return 'amber';
  if (['DISABLED', 'FAILED', 'CANCELLED', 'TIMEOUT', 'OUT_OF_MEMORY', 'DOWN', 'FAIL'].some((k) => s.includes(k))) return 'rose';
  if (['COMPLETED'].includes(s)) return 'idle';
  return 'cyan';
}

/** 硬件级拟物 LED 呼吸指示灯 */
export function LedBeacon({ tone = 'emerald', className = '' }: { tone?: LedTone; className?: string }) {
  return (
    <span className={`neu-led-beacon neu-led-${tone} ${className}`}>
      <span className="neu-led-core" />
    </span>
  );
}

/** 集成 LED 呼吸灯与内嵌药丸底托的状态徽章 */
export function StatusBadge({ status, tone }: { status: string; tone?: LedTone }) {
  const currentTone = tone || resolveLedTone(status);
  return (
    <span className="neu-status-pill">
      <LedBeacon tone={currentTone} />
      <span>{status || '-'}</span>
    </span>
  );
}

/** 触感凹槽导轨发光硬件进度条 */
export function NeuProgressBar({
  label,
  value,
  total,
  unit = '',
  color = 'cyan',
  showPercent = true,
}: {
  label?: string;
  value: number;
  total: number;
  unit?: string;
  color?: 'cyan' | 'emerald' | 'amber' | 'rose' | 'violet';
  showPercent?: boolean;
}) {
  const pct = total > 0 ? Math.min(100, Math.max(0, Math.round((value / total) * 100))) : 0;

  return (
    <div className="neu-progress-slot">
      {label && (
        <div className="neu-progress-header">
          <span className="neu-progress-label">{label}</span>
          <span className="neu-progress-val">
            {value} / {total} {unit} {showPercent && `(${pct}%)`}
          </span>
        </div>
      )}
      <div className="neu-progress-track">
        <div className={`neu-progress-fill ${color}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

/** 触感机械分段控制器（底托凹槽 + 凸起浮雕滑块） */
export function NeuSegmented<T extends string>({
  options,
  value,
  onChange,
}: {
  options: { label: string; value: T }[];
  value: T;
  onChange: (val: T) => void;
}) {
  return (
    <div className="neu-segmented-tray">
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          className={`neu-segmented-btn ${value === opt.value ? 'active' : ''}`}
          onClick={() => onChange(opt.value)}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'grid', gap: '0.35rem', fontSize: '0.8rem', fontWeight: 600, color: 'var(--text-muted,#94a3b8)' }}>
      {label}
      {children}
    </label>
  );
}

export function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return (
    <div
      style={{
        padding: '0.75rem 1.1rem',
        color,
        background: bg,
        borderRadius: 10,
        marginBottom: '1.25rem',
        fontSize: '0.88rem',
        border: '1px solid rgba(148,163,184,0.15)',
        boxShadow: 'var(--shadow-btn)',
        display: 'flex',
        alignItems: 'center',
        gap: '0.5rem',
      }}
    >
      {children}
    </div>
  );
}

export function MiniBtn({ disabled, onClick, children }: { disabled?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="neu-btn"
      style={{
        padding: '0.28rem 0.65rem',
        fontSize: '0.75rem',
        borderRadius: 7,
        cursor: disabled ? 'wait' : 'pointer',
      }}
    >
      {children}
    </button>
  );
}
