import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { slurm, oidc, oidcLoginURL, type SessionEntry } from '../services/slurm';
import { getStoredUser } from '../services/auth';

// A1 设置页：改密（复杂度策略 + 历史 N 次不可重用）、会话台账、全设备登出、
// SSO 绑定/解绑（S4）。
export const Route = createFileRoute('/settings')({ component: SettingsPage });

function SettingsPage() {
  const navigate = useNavigate();
  const [oldPw, setOldPw] = useState('');
  const [newPw, setNewPw] = useState('');
  const [newPw2, setNewPw2] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [sessions, setSessions] = useState<SessionEntry[]>([]);
  const [oidcEnabled, setOidcEnabled] = useState(false);
  const [oidcLinked, setOidcLinked] = useState(false);

  const user = getStoredUser();

  const loadSessions = useCallback(async () => {
    try {
      const r = await slurm.getMySessions();
      setSessions(r.sessions || []);
    } catch {
      /* 台账不可用（旧后端）时静默 */
    }
  }, []);

  useEffect(() => {
    loadSessions();
    oidc.config().then((c) => setOidcEnabled(!!c.enabled)).catch(() => {});
    slurm.getMe().then((me) => setOidcLinked(!!me.user.oidcLinked)).catch(() => {});
  }, [loadSessions]);

  const changePw = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    setInfo('');
    if (newPw !== newPw2) {
      setError('两次输入的新密码不一致');
      return;
    }
    setBusy(true);
    try {
      await slurm.changePassword(oldPw, newPw);
      setInfo('密码已更新，所有会话已失效，请重新登录');
      localStorage.removeItem('ails_user');
      setTimeout(() => navigate({ to: '/login' }), 1200);
    } catch (err: any) {
      setError(err?.message || String(err));
    } finally {
      setBusy(false);
    }
  };

  const logoutAllDevices = async () => {
    setError('');
    setInfo('');
    try {
      await slurm.logoutAll();
      localStorage.removeItem('ails_user');
      navigate({ to: '/login' });
    } catch (err: any) {
      setError(err?.message || String(err));
    }
  };

  const unlinkSSO = async () => {
    setError('');
    setInfo('');
    try {
      await oidc.unlink();
      setOidcLinked(false);
      setInfo('SSO 身份已解绑');
    } catch (err: any) {
      setError(err?.message || String(err));
    }
  };

  const cardStyle = {
    background: 'var(--bg-card,#1b1e28)',
    border: '1px solid var(--border-color,#2a2f3a)',
    borderRadius: 12,
    padding: '1.25rem',
    marginBottom: '1.5rem',
    boxShadow: 'var(--shadow-card)',
  } as const;
  const th = { padding: '0.6rem 0.8rem', fontSize: '0.72rem', textTransform: 'uppercase' as const, letterSpacing: '0.05em', color: 'var(--text-muted,#94a3b8)', fontWeight: 700, borderBottom: '1px solid var(--border-color,#2a2f3a)' };
  const td = { padding: '0.6rem 0.8rem', fontSize: '0.84rem', color: 'var(--text-main,#f1f5f9)' };

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1rem' }}>安全设置</h2>

      {error && <div style={{ padding: '0.6rem 0.9rem', color: '#f43f5e', background: 'rgba(239,68,68,.1)', borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{error}</div>}
      {info && <div style={{ padding: '0.6rem 0.9rem', color: '#10b981', background: 'rgba(16,185,129,.1)', borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{info}</div>}

      <form onSubmit={changePw} style={cardStyle}>
        <div style={{ fontSize: '1rem', fontWeight: 700, marginBottom: '0.5rem' }}>修改密码</div>
        <p style={{ margin: '0 0 0.75rem', fontSize: '0.78rem', color: 'var(--text-muted,#94a3b8)' }}>
          策略：至少 8 字符，须包含大写、小写、数字与符号；最近 5 次用过的密码不可重用。改密后所有设备需重新登录。
        </p>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: '0.75rem' }}>
          <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
            当前密码
            <input className="form-control" type="password" value={oldPw} onChange={(e) => setOldPw(e.target.value)} required />
          </label>
          <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
            新密码
            <input className="form-control" type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} required />
          </label>
          <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
            确认新密码
            <input className="form-control" type="password" value={newPw2} onChange={(e) => setNewPw2(e.target.value)} required />
          </label>
        </div>
        <button className="btn-primary" type="submit" disabled={busy} style={{ justifySelf: 'start', padding: '0.5rem 1.5rem', marginTop: '0.75rem' }}>
          {busy ? '提交中…' : '修改密码'}
        </button>
      </form>

      {oidcEnabled && (
        <div style={cardStyle}>
          <div style={{ fontSize: '1rem', fontWeight: 700, marginBottom: '0.5rem' }}>SSO 单点登录</div>
          <p style={{ margin: '0 0 0.75rem', fontSize: '0.78rem', color: 'var(--text-muted,#94a3b8)' }}>
            {oidcLinked
              ? `当前账号已绑定 SSO 身份（${user?.name || ''}），可用 SSO 或密码登录。`
              : '绑定企业 SSO 身份后，可用 SSO 直接登录本账号。'}
          </p>
          <div style={{ display: 'flex', gap: '0.6rem' }}>
            {!oidcLinked ? (
              <a className="btn-primary" href={oidcLoginURL(true)} style={{ padding: '0.5rem 1.2rem', textDecoration: 'none' }}>
                绑定 SSO 身份
              </a>
            ) : (
              <button
                onClick={unlinkSSO}
                style={{ padding: '0.5rem 1.2rem', borderRadius: 8, border: 'none', background: 'var(--card-bg)', boxShadow: 'var(--shadow-btn)', color: 'var(--accent-rose)', cursor: 'pointer' }}
              >
                解绑 SSO 身份
              </button>
            )}
          </div>
        </div>
      )}

      <div style={cardStyle}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.5rem' }}>
          <div style={{ fontSize: '1rem', fontWeight: 700 }}>当前有效会话</div>
          <button onClick={loadSessions} className="btn-primary" style={{ padding: '0.3rem 0.9rem' }}>刷新</button>
        </div>
        {sessions.length === 0 ? (
          <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.85rem' }}>暂无台账（旧后端或 OIDC 登录路径）</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ textAlign: 'left' }}>
                  <th style={th}>签发时间</th>
                  <th style={th}>过期时间</th>
                  <th style={th}>来源 IP</th>
                  <th style={th}>客户端</th>
                </tr>
              </thead>
              <tbody>
                {sessions.map((s) => (
                  <tr key={s.id}>
                    <td style={td}>{s.issuedAt?.replace('T', ' ') || '-'}</td>
                    <td style={td}>{s.expiresAt?.replace('T', ' ') || '-'}</td>
                    <td style={td}>{s.ip || '-'}</td>
                    <td style={{ ...td, maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={s.userAgent}>
                      {s.userAgent || '-'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div style={{ marginTop: '0.9rem' }}>
          <button
            onClick={logoutAllDevices}
            style={{ padding: '0.5rem 1.2rem', borderRadius: 8, border: 'none', background: 'var(--card-bg)', boxShadow: 'var(--shadow-btn)', color: 'var(--accent-rose)', cursor: 'pointer', fontWeight: 700 }}
          >
            全设备登出（吊销所有会话）
          </button>
        </div>
      </div>
    </div>
  );
}
