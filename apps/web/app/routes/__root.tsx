import { Outlet, Link, createRootRoute, useNavigate, useLocation } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import '../styles/app.css';
import { can, refreshMe, type StoredUser } from '../services/auth';
import { GlobalTooltip } from '../components/tooltip';

export const Route = createRootRoute({
  component: RootLayout,
});

function RootLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const [user, setUser] = useState<StoredUser | null>(null);
  const [theme, setTheme] = useState<'light' | 'dark'>('light');

  useEffect(() => {
    // 1. Restore User Auth
    const savedUser = localStorage.getItem('ails_user');
    if (savedUser) {
      setUser(JSON.parse(savedUser));
      // R4：挂载时按 /auth/me 刷新权限面（角色改派/权限调整后刷新页面即感知，静默失败）
      refreshMe().then((u) => u && setUser(u));
    } else if (!isAuthPage()) {
      navigate({ to: '/login' });
    }

    // 2. Restore Theme Preference (Default: Light / Soft Neumorphic)
    const savedTheme = (localStorage.getItem('ails_theme') as 'light' | 'dark') || 'light';
    setTheme(savedTheme);
    document.documentElement.setAttribute('data-theme', savedTheme);
  }, [location.pathname]);

  const toggleTheme = () => {
    const nextTheme = theme === 'light' ? 'dark' : 'light';
    setTheme(nextTheme);
    localStorage.setItem('ails_theme', nextTheme);
    document.documentElement.setAttribute('data-theme', nextTheme);
  };

  const handleHardRefresh = () => {
    window.location.reload();
  };

  // 登录页与其子路由（/login/oidc/callback）都不渲染侧栏布局
  const isAuthPage = () =>
    location.pathname === '/login' || location.pathname.startsWith('/login/');

  if (isAuthPage()) {
    return <Outlet />;
  }

  const handleLogout = () => {
    localStorage.removeItem('ails_user');
    navigate({ to: '/login' });
  };

  return (
    <div className="app-layout">
      <GlobalTooltip />
      <aside className="sidebar">
        <div>
          <div className="logo-brand">
            <div className="logo-badge">AILS</div>
            <span>Slurm HPC 管理平台</span>
          </div>
          <ul className="nav-list">
            {/* R4：菜单按权限点渲染（能力驱动；后端 RequirePermission 为权威门） */}
            {can('cluster:read', user) && (
              <>
                <li className="nav-item">
                  <Link to="/" activeProps={{ className: 'active' }} activeOptions={{ exact: true }}>
                    集群总览
                  </Link>
                </li>
                <li className="nav-item">
                  <Link to="/monitor" activeProps={{ className: 'active' }}>
                    集群监控
                  </Link>
                </li>
                <li className="nav-item">
                  <Link to="/nodes" activeProps={{ className: 'active' }}>
                    节点状态
                  </Link>
                </li>
                <li className="nav-item">
                  <Link to="/jobs" activeProps={{ className: 'active' }}>
                    作业管理
                  </Link>
                </li>
                <li className="nav-item">
                  <Link to="/history" activeProps={{ className: 'active' }}>
                    作业历史
                  </Link>
                </li>
                <li className="nav-item">
                  <Link to="/scheduler" activeProps={{ className: 'active' }}>
                    调度管理
                  </Link>
                </li>
              </>
            )}
            {can('ide:list', user) && (
              <li className="nav-item">
                <Link to="/webide" activeProps={{ className: 'active' }}>
                  Web-IDE
                </Link>
              </li>
            )}
            {can('billing:read', user) && (
              <li className="nav-item">
                <Link to="/billing" activeProps={{ className: 'active' }}>
                  计费
                </Link>
              </li>
            )}
            {/* 用户管理：平台面板（tenants:read）或租户面板（tenant:users:read）任一可见 */}
            {(can('tenants:read', user) || can('tenant:users:read', user)) && (
              <li className="nav-item">
                <Link to="/admin" activeProps={{ className: 'active' }}>
                  用户管理
                </Link>
              </li>
            )}
            {/* RBAC 管理（2026-08-19 IA 重组）：角色管理或审计任一可见 */}
            {(can('roles:manage', user) || can('tenant:roles:manage', user) || can('audit:read', user)) && (
              <li className="nav-item">
                <Link to="/rbac" activeProps={{ className: 'active' }}>
                  RBAC 管理
                </Link>
              </li>
            )}
            <li className="nav-item">
              <Link to="/settings" activeProps={{ className: 'active' }}>
                安全设置
              </Link>
            </li>
          </ul>
        </div>

        <div>
          {/* 刷新与主题控制 */}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.5rem', marginBottom: '1rem' }}>
            <button
              className="theme-toggle-btn"
              style={{ marginBottom: 0, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', gap: '0.35rem' }}
              onClick={toggleTheme}
              data-tooltip="切换明亮 / 暗黑模式"
            >
              {theme === 'light' ? (
                <>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                    <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
                  </svg>
                  <span>暗黑</span>
                </>
              ) : (
                <>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                    <circle cx="12" cy="12" r="5" />
                    <line x1="12" y1="1" x2="12" y2="3" />
                    <line x1="12" y1="21" x2="12" y2="23" />
                    <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
                    <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
                    <line x1="1" y1="12" x2="3" y2="12" />
                    <line x1="21" y1="12" x2="23" y2="12" />
                    <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
                    <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
                  </svg>
                  <span>明亮</span>
                </>
              )}
            </button>
            <button
              className="theme-toggle-btn"
              style={{ marginBottom: 0, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', gap: '0.35rem', color: 'var(--accent-primary)' }}
              onClick={handleHardRefresh}
              data-tooltip="刷新面板数据"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                <path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
                <path d="M21 3v5h-5" />
                <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
                <path d="M8 16H3v5" />
              </svg>
              <span>刷新</span>
            </button>
          </div>

          <div className="user-card" style={{ marginBottom: '1rem' }}>
            <div className="user-info">
              <span className="user-name">{user?.name || '高级算力管理员'}</span>
              <span className="user-role">{user?.org || 'hpc-lab'} ({user?.roleName || user?.role || '管理员'})</span>
            </div>
            <button
              style={{ background: 'none', border: 'none', color: 'var(--accent-rose)', cursor: 'pointer', fontSize: '0.8rem', fontWeight: 600 }}
              onClick={handleLogout}
            >
              退出
            </button>
          </div>
          <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
            <div>环境：单物理节点 Slurm 集群</div>
            <div>后端：Slurm 21.08 · slurmrestd v0.0.37</div>
          </div>
        </div>
      </aside>
      <main className="main-content">
        <Outlet />
      </main>
    </div>
  );
}
