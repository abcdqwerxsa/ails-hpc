import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { slurm, oidc, type SessionEntry, type PATInfo } from '../services/slurm';
import { getStoredUser } from '../services/auth';

// A1 设置页：改密（复杂度策略 + 历史 N 次不可重用）、会话台账、全设备登出、
// SSO 绑定/解绑（S4）、API 令牌（T1）。
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
  const [pats, setPats] = useState<PATInfo[]>([]);
  const [patName, setPatName] = useState('');
  const [patDays, setPatDays] = useState('');
  const [patBusy, setPatBusy] = useState(false);
  const [freshPat, setFreshPat] = useState<{ token: string; name: string } | null>(null);

  const user = getStoredUser();

  const loadSessions = useCallback(async () => {
    try {
      const r = await slurm.getMySessions();
      setSessions(r.sessions || []);
    } catch {
      /* 台账不可用（旧后端）时静默 */
    }
  }, []);

  const loadPats = useCallback(async () => {
    try {
      const r = await slurm.listPATs();
      setPats(r.tokens || []);
    } catch {
      /* PAT 面不可用（yaml 模式旧后端）时静默 */
    }
  }, []);

  useEffect(() => {
    loadSessions();
    loadPats();
    oidc.config().then((c) => setOidcEnabled(!!c.enabled)).catch(() => {});
    slurm.getMe().then((me) => setOidcLinked(!!me.user.oidcLinked)).catch(() => {});
  }, [loadSessions, loadPats]);

  const createPat = async () => {
    setError('');
    setInfo('');
    setFreshPat(null);
    const days = parseInt(patDays, 10);
    if (patDays !== '' && (isNaN(days) || days < 1 || days > 3650)) {
      setError('有效期须为 1-3650 天，或留空表示长期');
      return;
    }
    setPatBusy(true);
    try {
      const r = await slurm.createPAT({
        name: patName.trim() || undefined,
        expiresInDays: days || undefined,
      });
      setFreshPat({ token: r.token, name: r.name });
      setPatName('');
      setPatDays('');
      await loadPats();
    } catch (err: any) {
      setError(err?.message || String(err));
    } finally {
      setPatBusy(false);
    }
  };

  const revokePat = async (id: number, name: string) => {
    if (!confirm(`吊销令牌「${name}」？使用它的脚本将立即失效。`)) return;
    setError('');
    setInfo('');
    try {
      await slurm.revokePAT(id);
      setInfo(`令牌「${name}」已吊销`);
      await loadPats();
    } catch (err: any) {
      setError(err?.message || String(err));
    }
  };

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

  // 绑定走认证 XHR 取 authorize URL 再整页导航（<a> 直链带不上 JWT 头）
  const bindSSO = async () => {
    setError('');
    setInfo('');
    try {
      const r = await oidc.bind();
      if (r.authorizeUrl) window.location.href = r.authorizeUrl;
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
              <button className="btn-primary" onClick={bindSSO} style={{ padding: '0.5rem 1.2rem' }}>
                绑定 SSO 身份
              </button>
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
        <div style={{ fontSize: '1rem', fontWeight: 700, marginBottom: '0.5rem' }}>API 令牌</div>
        <p style={{ margin: '0 0 0.75rem', fontSize: '0.78rem', color: 'var(--text-muted,#94a3b8)' }}>
          供脚本/cron 调用面板 API（<code>Authorization: Bearer ailspat_…</code>），免用账号密码登录。
          明文仅在创建时显示一次；吊销即刻生效。最多 10 个活跃令牌。
        </p>
        {freshPat && (
          <div style={{ padding: '0.75rem 0.9rem', background: 'rgba(59,130,246,.1)', border: '1px solid var(--accent-primary,#3b82f6)', borderRadius: 8, marginBottom: '0.9rem' }}>
            <div style={{ fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)', marginBottom: '0.35rem' }}>
              令牌「{freshPat.name}」已创建——<b>关闭后不再显示，请立即保存</b>：
            </div>
            <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
              <code style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.78rem', wordBreak: 'break-all', flex: 1 }}>{freshPat.token}</code>
              <button onClick={() => navigator.clipboard?.writeText(freshPat.token)} style={{ padding: '0.25rem 0.7rem', borderRadius: 6, border: 'none', background: 'var(--card-bg)', boxShadow: 'var(--shadow-btn)', color: 'var(--text-main,#f1f5f9)', cursor: 'pointer', fontSize: '0.75rem' }}>
                复制
              </button>
            </div>
          </div>
        )}
        <div style={{ display: 'grid', gridTemplateColumns: 'minmax(160px,1fr) 120px auto', gap: '0.6rem', marginBottom: '0.9rem' }}>
          <input className="form-control" value={patName} onChange={(e) => setPatName(e.target.value)} placeholder="令牌名称（如 ci-deploy）" />
          <input className="form-control" value={patDays} onChange={(e) => setPatDays(e.target.value)} placeholder="有效天数（空=长期）" />
          <button className="btn-primary" onClick={createPat} disabled={patBusy} style={{ padding: '0.45rem 1.1rem' }}>
            {patBusy ? '创建中…' : '创建令牌'}
          </button>
        </div>
        {pats.length === 0 ? (
          <div style={{ color: 'var(--text-muted,#94a3b8)', fontSize: '0.85rem' }}>暂无令牌</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ textAlign: 'left' }}>
                  <th style={th}>名称</th><th style={th}>前缀</th><th style={th}>创建</th>
                  <th style={th}>最近使用</th><th style={th}>过期</th><th style={th}>状态</th><th style={th}>操作</th>
                </tr>
              </thead>
              <tbody>
                {pats.map((p) => (
                  <tr key={p.id}>
                    <td style={td}>{p.name}</td>
                    <td style={{ ...td, fontFamily: "'JetBrains Mono', monospace", fontSize: '0.78rem' }}>{p.prefix}…</td>
                    <td style={td}>{p.createdAt?.slice(0, 16) || '-'}</td>
                    <td style={td}>{p.lastUsedAt?.slice(0, 16) || '从未'}</td>
                    <td style={td}>{p.expiresAt?.slice(0, 10) || '长期'}</td>
                    <td style={td}>{p.revoked ? <span style={{ color: '#f43f5e' }}>已吊销</span> : <span style={{ color: '#10b981' }}>活跃</span>}</td>
                    <td style={td}>
                      {!p.revoked && (
                        <button onClick={() => revokePat(p.id, p.name)} style={{ padding: '0.2rem 0.6rem', borderRadius: 6, border: 'none', background: 'var(--card-bg)', boxShadow: 'var(--shadow-btn)', color: 'var(--accent-rose)', cursor: 'pointer', fontSize: '0.75rem' }}>
                          吊销
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

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
