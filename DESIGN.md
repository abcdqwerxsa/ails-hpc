---
name: slurm-manager-design-system
version: 1.0.0
description: 1:1 Neumorphic Dual-Theme Masterpiece Design System for Slurm Cluster Manager
colors:
  primary: "#06B6D4"
  bg-main: "#14161E"
  bg-sidebar: "#161821"
  card-bg: "#1B1E28"
  text-primary: "#F1F5F9"
  text-secondary: "#94A3B8"
  text-dim: "#556377"
  accent-cyan: "#06B6D4"
  accent-emerald: "#10B981"
  accent-amber: "#F59E0B"
  accent-rose: "#F43F5E"
shadows:
  card: "-2px -2px 4px rgba(255, 255, 255, 0.16), -14px -14px 28px rgba(255, 255, 255, 0.035), 4px 4px 8px rgba(0, 0, 0, 0.7), 14px 14px 28px #08090C"
  inset-deep: "inset 4px 4px 8px rgba(0, 0, 0, 0.85), inset 10px 10px 22px #08090C, inset -2px -2px 4px rgba(255, 255, 255, 0.1), inset -10px -10px 20px rgba(255, 255, 255, 0.04)"
rounded:
  panel: "15px"
  chip: "6px"
  tag: "10px"
spacing:
  gap: "20px"
  sidebar-width: "230px"
typography:
  body:
    fontFamily: "Inter, sans-serif"
    fontSize: "13px"
    fontWeight: "600"
---

# SLURM CLUSTER MANAGER - NEUMORPHIC DESIGN SYSTEM (DESIGN.md)

> 本设计规范由 Google DeepMind Agentic Design Guidelines 归纳总结，记录了 **Slurm Cluster Manager** 高级工控面板的轻拟物 (Soft Neumorphism) 双主题界面设计体系。
> 方便后续团队或 AI Agent 快速复线与再次生成：可随时结合 `npx @google/design.md` 提取并复用本设计标准。

---

## 1. 核心设计哲学 (Core Design Philosophy)

### 1.1 纯正的物理基底统一法则 (Unified Surface Palette)
- **统一 Surface 背景色**：凸起面板 (Raised Cards) 与 凹陷下沉槽 (Sunken Slots) **100% 共享完全相同的表面底色 `var(--card-bg)`**（暗色模式：`#1B1E28`；浅色模式：`#EAEFF5`）。
- **杜绝切边与生硬混色**：绝对禁止在凹陷组件上强制填充死黑色或割裂颜色。下陷与凸起的效果**完全靠多阶 Shadow 的正负坐标偏移与 135° 光源伪边框的倒转**来实现。

### 1.2 135° 主光源与物理互逆双生 (Top-Left Light Duality)
光源方向恒定设定为**左上方 (135° Vector)**：
- **凸起 (Embossed / Raised)**：
  - 左上角：呈现白微光溢出与亮白弥散辉光（`-X, -Y` 负坐标）。
  - 右下角：投射深沉沉沉暗影（`+X, +Y` 正坐标）。
  - 伪边框：`linear-gradient(135deg, white-glow 0%, dark-drop 100%)`。
- **凹陷 (Recessed / Sunken)**：
  - 左上角：呈现黑洞般深刻内落影（`+X, +Y` 负坐标内陷）。
  - 右下角：呈现白光月牙反射弧（`-X, -Y` 正坐标内陷）。
  - 伪边框：`linear-gradient(135deg, dark-shadow 0%, white-reflection 100%)`。

---

## 2. 设计令牌 (Design Tokens & Color Palette)

```css
:root {
  /* 基础壁板与调和卡片色 */
  --bg-main: #14161E;       /* 大盘背景板 */
  --bg-sidebar: #161821;    /* 侧边栏壁板 */
  --card-bg: #1B1E28;       /* 凸起/凹陷 100% 统一共享卡片底色 */
  
  --text-primary: #F1F5F9;
  --text-secondary: #94A3B8;
  --text-dim: #556377;

  /* 1. 凸起 4 重多阶物理阴影 */
  --shadow-card: -2px -2px 4px rgba(255, 255, 255, 0.16), -14px -14px 28px rgba(255, 255, 255, 0.035), 4px 4px 8px rgba(0, 0, 0, 0.7), 14px 14px 28px #08090C;
  --shadow-card-hover: -2px -2px 5px rgba(255, 255, 255, 0.2), -16px -16px 32px rgba(255, 255, 255, 0.05), 5px 5px 10px rgba(0, 0, 0, 0.8), 18px 18px 34px #060709;
  --shadow-btn: -1.5px -1.5px 3px rgba(255, 255, 255, 0.16), -7px -7px 16px rgba(255, 255, 255, 0.035), 3px 3px 6px rgba(0, 0, 0, 0.7), 8px 8px 18px #08090C;
  --shadow-btn-active: inset 4px 4px 8px #08090C, inset -4px -4px 8px rgba(255, 255, 255, 0.04);
  --sidebar-shadow: 10px 0 24px #07080B;

  /* 2. 凹陷 4 重镜像倒转内深槽阴影 */
  --shadow-inset-deep: inset 4px 4px 8px rgba(0, 0, 0, 0.85), inset 10px 10px 22px #08090C, inset -2px -2px 4px rgba(255, 255, 255, 0.1), inset -10px -10px 20px rgba(255, 255, 255, 0.04);

  /* 霓虹发光 Token */
  --accent-cyan: #06B6D4;
  --accent-cyan-glow: 0 0 14px rgba(6, 182, 212, 0.7);
  --accent-emerald: #10B981;
  --accent-emerald-glow: 0 0 14px rgba(16, 185, 129, 0.7);
  --accent-amber: #F59E0B;
  --accent-amber-glow: 0 0 12px rgba(245, 158, 11, 0.6);
  --accent-rose: #F43F5E;
  --accent-rose-glow: 0 0 12px rgba(244, 63, 94, 0.6);
}

[data-theme="light"] {
  /* 浅色羊脂玉极光模式 */
  --bg-main: #F0F3F8;
  --bg-sidebar: #E9EEF5;
  --card-bg: #EAEFF5;
  --text-primary: #1E293B;
  --text-secondary: #64748B;
  --text-dim: #94A3B8;

  --shadow-card: -3px -3px 6px #FFFFFF, -12px -12px 24px #FFFFFF, 3px 3px 6px rgba(166, 180, 200, 0.4), 12px 12px 24px #D1D9E6;
  --shadow-btn: -2px -2px 4px #FFFFFF, -7px -7px 15px #FFFFFF, 2px 2px 4px rgba(166, 180, 200, 0.4), 7px 7px 15px #D1D9E6;
  --sidebar-shadow: 6px 0 20px rgba(166, 180, 200, 0.25);
  --shadow-inset-deep: inset 4px 4px 8px rgba(166, 180, 200, 0.5), inset 10px 10px 20px #CBD5E1, inset -2px -2px 4px #FFFFFF, inset -10px -10px 20px #FFFFFF;
}
```

---

## 3. 布局与圆角规范 (Layout & Crisp Radii)

### 3.1 硬朗工控圆角规范 (Crisp Radii)
- **搜索框 (`.search-box`)**：`9px` 精干工业圆角。
- **状态标签 (`.status-tag`) & Icon 按钮**：`10px`。
- **核心面板 (`.neu-panel`) & 仪表卡片 (`.gauge-card`)**：`15px` 利落圆角。
- **节点方块 (`.node-small-chip`)**：`6px` 触感按压芯片。

### 3.2 50% 网格宽幅分配 (50% Grid Ratio)
Overview 下半部分采用 4 栏弹性占比，确保左侧**节点矩阵 (`NODE STATUS GRID`)** + **作业队列 (`JOB QUEUE`)** 精确占据右侧主功能区宽度的 **50%**：

```css
.full-grid-layout {
  display: grid;
  grid-template-columns: 2.2fr 2.8fr 2.5fr 2.5fr;
  gap: 20px;
  width: 100%;
}
```

---

## 4. 严禁事项 (Anti-Patterns & Strict Bans)

1. ❌ **严禁使用 AI Emoji 作为 UI Icon**：所有图标必须使用 SVG 矢量几何线条或 SVG 点阵 Logo。
2. ❌ **严禁添加 Hard Border 线框**：切勿使用 `border: 1px solid #xxx` 强硬框线，破坏拟物阴影过渡。
3. ❌ **严禁给凹陷元素设置死黑背景**：凹陷元素必须使用共享的 `var(--card-bg)`，靠 Inset 阴影沉降。
4. ❌ **严禁全局出现溢出滚动条**：Node Status Grid 必须整齐扩展显示，页面强制 `overflow-x: hidden`。

---

## 5. 如何复用本设计规范 (Reusability & Tooling)

您可以在后续的任何前端开发或 UI 重构中通过以下方式复线本系统：
1. 直接在 CLI 中运行：`npx @google/design.md lint DESIGN.md` 校验结构合法性。
2. 运行 `npx @google/design.md export --format css-vars DESIGN.md` 自动导出 CSS Token 变量。
