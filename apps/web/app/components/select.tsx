// 自定义下拉组件：触发器 + 页面内渲染的弹层，完全不依赖原生 <select>。
// 动机：原生弹层在 Firefox / Chrome<135 上无法接管（base-select 未普及），
// color-scheme 也只能整体染暗——跨浏览器唯一确定的路是全部自己画。
// 样式复用 .form-control 的拟物变量（卡片底/内嵌阴影/青色描边），弹层高 z-index。
import { useEffect, useRef, useState, type CSSProperties } from 'react';

export interface SelectOption {
  value: string;
  label: string;
}

export function Select({
  value,
  onChange,
  options,
  width,
  small,
  style,
  ariaLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  options: SelectOption[];
  width?: number | string;
  small?: boolean;
  style?: CSSProperties;
  ariaLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // 点击外部 / Esc 关闭
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const current = options.find((o) => o.value === value);
  return (
    <div ref={rootRef} style={{ position: 'relative', width: width ?? '100%', ...style }}>
      <div
        role="combobox"
        aria-expanded={open}
        aria-label={ariaLabel}
        tabIndex={0}
        className={`form-control${small ? ' form-control-sm' : ''}`}
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '0.5rem',
          cursor: 'pointer',
          userSelect: 'none',
          width: '100%',
          boxSizing: 'border-box',
          borderColor: open ? 'rgba(6, 182, 212, 0.55)' : undefined,
        }}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            setOpen((o) => !o);
          }
        }}
      >
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {current?.label ?? value}
        </span>
        <svg
          viewBox="0 0 24 24"
          width={small ? 13 : 16}
          height={small ? 13 : 16}
          style={{ flexShrink: 0, transform: open ? 'rotate(180deg)' : 'none', transition: 'transform .15s ease' }}
        >
          <path fill="#06B6D4" d="M7 10l5 5 5-5z" />
        </svg>
      </div>
      {open && (
        <ul
          role="listbox"
          style={{
            position: 'absolute',
            top: 'calc(100% + 0.35rem)',
            left: 0,
            right: 0,
            margin: 0,
            padding: '0.4rem',
            listStyle: 'none',
            background: 'var(--card-bg,#1b1e28)',
            borderRadius: 'var(--radius-md,10px)',
            boxShadow: 'var(--shadow-card)',
            zIndex: 1000,
            maxHeight: 260,
            overflowY: 'auto',
          }}
        >
          {options.map((o) => {
            const sel = o.value === value;
            return (
              <li
                key={o.value}
                role="option"
                aria-selected={sel}
                style={{
                  padding: small ? '0.35rem 0.6rem' : '0.5rem 0.7rem',
                  borderRadius: 'var(--radius-sm,8px)',
                  fontSize: small ? '0.8rem' : '0.875rem',
                  cursor: 'pointer',
                  color: sel ? 'var(--accent-cyan,#06B6D4)' : 'var(--text-main,#f1f5f9)',
                  fontWeight: sel ? 600 : 400,
                  background: sel ? 'rgba(6,182,212,0.12)' : 'transparent',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                }}
                onClick={() => {
                  onChange(o.value);
                  setOpen(false);
                }}
                onMouseEnter={(e) => {
                  if (!sel) e.currentTarget.style.background = 'rgba(241,245,249,0.07)';
                }}
                onMouseLeave={(e) => {
                  if (!sel) e.currentTarget.style.background = 'transparent';
                }}
              >
                {o.label}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
