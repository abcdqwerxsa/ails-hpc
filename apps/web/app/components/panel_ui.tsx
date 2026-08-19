// 管理类面板共享的小组件与样式常量（自 admin.tsx 抽出——2026-08-19 IA 重组：
// 用户管理/RBAC 管理/调度管理三页共用，定义单一来源）。
import type { ReactNode } from 'react';

export const cardStyle = {
  background: 'var(--bg-card,#1b1e28)',
  border: '1px solid var(--border-color,#2a2f3a)',
  borderRadius: 12,
  padding: '1.25rem',
  display: 'grid',
  gap: '0.75rem',
  boxShadow: 'var(--shadow-card)',
  transition: 'box-shadow .3s ease',
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

// 状态徽章配色：active=emerald / suspended=amber / disabled=rose（其余蓝灰兜底）
const STATUS_STYLES: Record<string, { color: string; bg: string }> = {
  active: { color: '#10b981', bg: 'rgba(16,185,129,.12)' },
  suspended: { color: '#f59e0b', bg: 'rgba(245,158,11,.12)' },
  disabled: { color: '#f43f5e', bg: 'rgba(244,63,94,.12)' },
};

export function StatusBadge({ status }: { status: string }) {
  const s = STATUS_STYLES[(status || '').toLowerCase()] || { color: '#3b82f6', bg: 'rgba(59,130,246,.12)' };
  return (
    <span style={{ padding: '0.15rem 0.5rem', borderRadius: 6, fontSize: '0.72rem', fontWeight: 700, color: s.color, background: s.bg }}>
      {status || '-'}
    </span>
  );
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
      {label}
      {children}
    </label>
  );
}

export function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{children}</div>;
}

export function MiniBtn({ disabled, onClick, children }: { disabled?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      style={{
        padding: '0.25rem 0.6rem',
        fontSize: '0.75rem',
        borderRadius: 6,
        border: 'none',
        background: 'var(--card-bg)',
        boxShadow: 'var(--shadow-btn)',
        color: 'var(--text-main,#f1f5f9)',
        cursor: disabled ? 'wait' : 'pointer',
        transition: 'box-shadow .2s ease',
      }}
    >
      {children}
    </button>
  );
}
