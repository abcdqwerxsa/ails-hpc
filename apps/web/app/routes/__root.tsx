import { Outlet, Link, createRootRoute, useNavigate, useLocation } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import '../styles/app.css';

export const Route = createRootRoute({
  component: RootLayout,
});

function RootLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const [user, setUser] = useState<any>(null);
  const [theme, setTheme] = useState<'light' | 'dark'>('light');

  useEffect(() => {
    // 1. Restore User Auth
    const savedUser = localStorage.getItem('ails_user');
    if (savedUser) {
      setUser(JSON.parse(savedUser));
    } else if (location.pathname !== '/login') {
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

  if (location.pathname === '/login') {
    return <Outlet />;
  }

  const handleLogout = () => {
    localStorage.removeItem('ails_user');
    navigate({ to: '/login' });
  };

  return (
    <div className="app-layout">
      <aside className="sidebar">
        <div>
          <div className="logo-brand">
            <div className="logo-badge">AILS</div>
            <span>Slurm HPC 管理平台</span>
          </div>
          <ul className="nav-list">
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
              <Link to="/webide" activeProps={{ className: 'active' }}>
                Web-IDE
              </Link>
            </li>
            <li className="nav-item">
              <Link to="/billing" activeProps={{ className: 'active' }}>
                计费
              </Link>
            </li>
            {/* 管理入口：仅 admin / tenant_admin 可见（角色来自 localStorage 'ails_user'，同 user 解析） */}
            {(user?.role === 'admin' || user?.role === 'tenant_admin') && (
              <li className="nav-item">
                <Link to="/admin" activeProps={{ className: 'active' }}>
                  管理
                </Link>
              </li>
            )}
            <li className="nav-item">
              <Link to="/partitions" activeProps={{ className: 'active' }}>
                分区
              </Link>
            </li>
          </ul>
        </div>

        <div>
          {/* 刷新与主题控制 */}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.5rem', marginBottom: '1rem' }}>
            <button className="theme-toggle-btn" style={{ marginBottom: 0 }} onClick={toggleTheme}>
              <span>{theme === 'light' ? '🌙 暗黑' : '☀️ 明亮'}</span>
            </button>
            <button className="theme-toggle-btn" style={{ marginBottom: 0, color: 'var(--accent-primary)' }} onClick={handleHardRefresh}>
              <span>🔄 强刷面版</span>
            </button>
          </div>

          <div className="user-card" style={{ marginBottom: '1rem' }}>
            <div className="user-info">
              <span className="user-name">{user?.name || '高级算力管理员'}</span>
              <span className="user-role">{user?.org || 'hpc-lab'} ({user?.role || '管理员'})</span>
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
