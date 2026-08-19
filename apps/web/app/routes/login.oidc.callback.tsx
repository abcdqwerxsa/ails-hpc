import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useEffect, useState } from 'react';
import { oidc } from '../services/slurm';
import { refreshMe } from '../services/auth';

// S3：OIDC 回调路由（hash: /login/oidc/callback?status=..&token=..）。
// 后端 302 时 token 只放在 URL 片段里——浏览器不会把 fragment 发给服务器，
// 也不进访问日志/Referer。
export const Route = createFileRoute('/login/oidc/callback')({
  component: OidcCallbackPage,
});

function parseHashQuery(): URLSearchParams {
  const h = window.location.hash;
  const i = h.indexOf('?');
  return new URLSearchParams(i >= 0 ? h.slice(i + 1) : '');
}

function OidcCallbackPage() {
  const navigate = useNavigate();
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [linkToken, setLinkToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');

  useEffect(() => {
    const q = parseHashQuery();
    const st = q.get('status') || '';
    setStatus(st);
    setError(q.get('error') || '');
    setLinkToken(q.get('token') || '');
    if (st === 'ok') {
      const token = q.get('token') || '';
      if (!token) {
        setError('回调缺少 token');
        return;
      }
      localStorage.setItem('ails_token', token);
      refreshMe().then(() => navigate({ to: '/' }));
    }
    if (st === 'bound') {
      setMsg('SSO 身份已绑定到当前账号');
      setTimeout(() => navigate({ to: '/' }), 1200);
    }
  }, [navigate]);

  const confirmLink = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      const r = await oidc.confirmLink({ linkToken, username, password });
      localStorage.setItem('ails_token', r.token);
      await refreshMe();
      navigate({ to: '/' });
    } catch (err: any) {
      setError('关联失败：' + (err?.message || err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-container">
      <div className="auth-card">
        <div className="auth-title">SSO 登录回调</div>

        {status === 'ok' && <div className="auth-sub">登录成功，正在进入控制台…</div>}
        {status === 'bound' && (
          <div className="auth-sub" style={{ color: '#10b981' }}>{msg || '绑定成功'}</div>
        )}
        {status === 'error' && (
          <>
            <div style={{ padding: '0.65rem 0.85rem', background: 'rgba(239,68,68,0.1)', color: 'var(--accent-rose)', borderRadius: 6, fontSize: '0.85rem', marginBottom: '1rem' }}>
              SSO 登录失败：{error || '未知错误'}
            </div>
            <button className="btn-primary" style={{ width: '100%', padding: '0.75rem' }} onClick={() => navigate({ to: '/login' })}>
              返回登录
            </button>
          </>
        )}

        {status === 'link' && (
          <>
            <div className="auth-sub">
              该 SSO 身份的用户名与一个本地账号同名。输入本地账号密码确认关联——
              关联后可用 SSO 或密码登录同一账号。
            </div>
            <form onSubmit={confirmLink}>
              <div className="form-group">
                <label>本地用户名</label>
                <input className="form-control" value={username} onChange={(e) => setUsername(e.target.value)} required />
              </div>
              <div className="form-group">
                <label>本地密码</label>
                <input className="form-control" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
              </div>
              {error && (
                <div style={{ padding: '0.5rem 0.7rem', background: 'rgba(239,68,68,0.1)', color: 'var(--accent-rose)', borderRadius: 6, fontSize: '0.8rem', marginBottom: '0.8rem' }}>
                  {error}
                </div>
              )}
              <button type="submit" className="btn-primary" style={{ width: '100%', padding: '0.75rem', marginTop: '0.5rem' }} disabled={busy}>
                {busy ? '正在确认…' : '确认并关联'}
              </button>
            </form>
            <button
              style={{ width: '100%', padding: '0.6rem', marginTop: '0.75rem', background: 'none', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '0.85rem' }}
              onClick={() => navigate({ to: '/login' })}
            >
              不关联，返回登录
            </button>
          </>
        )}
      </div>
    </div>
  );
}
