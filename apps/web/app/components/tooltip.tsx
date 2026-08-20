import { useEffect, useState } from 'react';

interface TooltipState {
  visible: boolean;
  text: string;
  x: number;
  y: number;
  placement: 'top' | 'bottom';
  arrowX: number;
}

/**
 * 全局拟物 Tooltip 组件
 * 自动监听全局所有带 `data-tooltip` 或 `title` 的元素，
 * 替换浏览器丑陋的原生系统浮层，以软拟物微光浮层渲染，
 * 随明暗主题自适应配色与阴影。
 */
export function GlobalTooltip() {
  const [state, setState] = useState<TooltipState>({
    visible: false,
    text: '',
    x: 0,
    y: 0,
    placement: 'top',
    arrowX: 50,
  });

  useEffect(() => {
    let timer: any = null;

    const onMouseOver = (e: MouseEvent) => {
      const target = (e.target as HTMLElement)?.closest?.('[data-tooltip], [title]') as HTMLElement | null;
      if (!target) return;

      // 转移原生 title 到 data-tooltip，防止浏览器弹出原生 OS 提示框
      let tooltipText = target.getAttribute('data-tooltip');
      if (!tooltipText && target.hasAttribute('title')) {
        const titleVal = target.getAttribute('title') || '';
        if (titleVal.trim()) {
          target.setAttribute('data-tooltip', titleVal);
          target.removeAttribute('title');
          tooltipText = titleVal;
        }
      }

      if (!tooltipText || !tooltipText.trim()) {
        setState((prev) => ({ ...prev, visible: false }));
        return;
      }

      clearTimeout(timer);
      timer = setTimeout(() => {
        const rect = target.getBoundingClientRect();
        const targetCenterX = rect.left + rect.width / 2;

        // 计算屏幕内安全坐标（水平居中并防溢出）
        const padding = 12;
        const estWidth = Math.min(320, Math.max(80, tooltipText.length * 13 + 24));
        const left = Math.max(padding, Math.min(window.innerWidth - estWidth - padding, targetCenterX - estWidth / 2));
        const arrowLeft = Math.max(10, Math.min(estWidth - 10, targetCenterX - left));

        // 顶部空间不足 48px 时向下弹出
        const preferTop = rect.top >= 48;
        const top = preferTop ? rect.top - 8 : rect.bottom + 8;
        const placement = preferTop ? 'top' : 'bottom';

        setState({
          visible: true,
          text: tooltipText,
          x: left,
          y: top,
          placement,
          arrowX: arrowLeft,
        });
      }, 80);
    };

    const onMouseOut = (e: MouseEvent) => {
      const related = e.relatedTarget as HTMLElement | null;
      const currentTarget = (e.target as HTMLElement)?.closest?.('[data-tooltip], [title]');
      if (!related || !currentTarget || !currentTarget.contains(related)) {
        clearTimeout(timer);
        setState((prev) => ({ ...prev, visible: false }));
      }
    };

    const dismiss = () => {
      clearTimeout(timer);
      setState((prev) => ({ ...prev, visible: false }));
    };

    document.addEventListener('mouseover', onMouseOver, true);
    document.addEventListener('mouseout', onMouseOut, true);
    window.addEventListener('scroll', dismiss, true);
    window.addEventListener('click', dismiss, true);

    return () => {
      clearTimeout(timer);
      document.removeEventListener('mouseover', onMouseOver, true);
      document.removeEventListener('mouseout', onMouseOut, true);
      window.removeEventListener('scroll', dismiss, true);
      window.removeEventListener('click', dismiss, true);
    };
  }, []);

  if (!state.visible || !state.text) return null;

  return (
    <div
      className={`global-tooltip visible arrow-${state.placement === 'top' ? 'bottom' : 'top'}`}
      style={{
        left: state.x,
        top: state.y,
        transform: state.placement === 'top' ? 'translateY(-100%)' : 'translateY(0)',
        ['--arrow-left' as any]: `${state.arrowX}px`,
      }}
    >
      {state.text}
      <div className="global-tooltip-arrow" />
    </div>
  );
}
