import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { slurm, oidc, oidcLoginURL } from '../services/slurm';

export const Route = createFileRoute('/login')({
  component: LoginPage,
});

function LoginPage() {
  const navigate = useNavigate();
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('admin123');
  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState('');
  const [ssoEnabled, setSsoEnabled] = useState(false);

  useEffect(() => {
    // S3：有 OIDC 配置才显示 SSO 按钮（公开配置端点告知）
    oidc.config().then((c) => setSsoEnabled(!!c.enabled)).catch(() => setSsoEnabled(false));
  }, []);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setErrorMsg('');

    try {
      const data = await slurm.login(username, password);
      localStorage.setItem('ails_token', data.token);
      // R4：存全量用户面（含权限点清单）——前端能力驱动（can()）的数据源
      localStorage.setItem(
        'ails_user',
        JSON.stringify({
          name: data.user.username,
          role: data.user.role,
          roleName: data.user.roleName || undefined,
          permissions: data.user.permissions || [],
          org: data.user.orgSlug,
          tenantNs: data.user.tenantNs,
          clusterUser: data.user.clusterUser,
        })
      );
      // A1：初始/被重置密码强制首登改密
      if (data.user.mustChangePassword) {
        navigate({ to: '/settings' });
        return;
      }
      navigate({ to: '/' });
    } catch (err: any) {
      setErrorMsg('系统连接失败: ' + err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="auth-container">
      <div className="auth-card">
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', marginBottom: '1.5rem' }}>
          <div className="logo-badge" style={{ width: '36px', height: '36px', fontSize: '1rem' }}>
            AILS
          </div>
          <span style={{ fontWeight: 700, fontSize: '1.25rem', color: 'var(--text-main)' }}>SSO 统一身份认证入口</span>
        </div>

        <div className="auth-title">登录云原生 HPC 算力控制台</div>
        <div className="auth-sub">包含企业级 SSO 单点登录映射与 RBAC 多租户权限系统</div>

        {errorMsg && (
          <div style={{ padding: '0.65rem 0.85rem', background: 'rgba(239, 68, 68, 0.1)', color: 'var(--accent-rose)', borderRadius: '6px', fontSize: '0.85rem', marginBottom: '1.25rem', border: '1px solid rgba(239, 68, 68, 0.25)' }}>
            {errorMsg}
          </div>
        )}

        <form onSubmit={handleLogin}>
          <div className="form-group">
            <label>用户账号 / Enterprise SSO ID</label>
            <input
              className="form-control"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="如 admin, hpc-researcher"
              required
            />
          </div>

          <div className="form-group">
            <label>登录密码</label>
            <input
              type="password"
              className="form-control"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </div>

          <button
            type="submit"
            className="btn-primary"
            style={{ width: '100%', padding: '0.75rem', marginTop: '1rem' }}
            disabled={loading}
          >
            {loading ? '正在验证...' : '账号密码登录'}
          </button>
        </form>

        {ssoEnabled && (
          <>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', margin: '1.25rem 0', color: 'var(--text-muted)', fontSize: '0.8rem' }}>
              <span style={{ flex: 1, height: 1, background: 'var(--border-color,#2a2f3a)' }} />
              或
              <span style={{ flex: 1, height: 1, background: 'var(--border-color,#2a2f3a)' }} />
            </div>
            <a
              href={oidcLoginURL()}
              className="btn-primary"
              style={{ display: 'block', width: '100%', padding: '0.75rem', textAlign: 'center', textDecoration: 'none', boxSizing: 'border-box' }}
            >
              使用企业 SSO 登录
            </a>
          </>
        )}
      </div>
    </div>
  );
}
