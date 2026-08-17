import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useEffect, useState, type ChangeEvent, type FormEvent, type ReactNode } from 'react';
import { slurm, type AdminUser, type TenantInfo } from '../services/slurm';

export const Route = createFileRoute('/admin')({ component: AdminPage });

// 登录角色存于 localStorage('ails_user').role（login 页写入，__root 侧栏同源解析）。
function readRole(): string {
  try {
    const raw = localStorage.getItem('ails_user');
    return raw ? String(JSON.parse(raw)?.role || '') : '';
  } catch {
    return '';
  }
}

// 单页双面板：admin → 平台管理（租户 + 平台用户）；tenant_admin → 本租户用户管理。
function AdminPage() {
  const [role] = useState(readRole);
  if (role === 'admin') return <PlatformAdminPanel />;
  if (role === 'tenant_admin') return <TenantUsersPanel />;
  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1rem' }}>管理</h2>
      <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">当前账号（{role || '未登录'}）无管理权限。</Notice>
    </div>
  );
}

// ---------- tenant_admin：租户用户管理 ----------

const emptyUserForm = { username: '', password: '', role: 'member' };

function TenantUsersPanel() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [form, setForm] = useState(emptyUserForm);
  const [submitting, setSubmitting] = useState(false);
  const [acting, setActing] = useState(''); // `<username>:status` | `<username>:pw`
  const [resetFor, setResetFor] = useState(''); // 正在重置密码的用户名（空 = 未展开）
  const [resetPw, setResetPw] = useState('');

  const refresh = useCallback(async () => {
    try {
      const r = await slurm.listMyTenantUsers();
      setUsers(r.users || []);
      setError('');
    } catch (e: any) {
      setError(e?.message || '加载租户用户失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const field = (k: keyof typeof emptyUserForm) => (e: ChangeEvent<HTMLInputElement>) =>
    setForm({ ...form, [k]: e.target.value });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form.username.trim() || !form.password) {
      setError('用户名与密码必填');
      return;
    }
    setSubmitting(true);
    setError('');
    setInfo('');
    try {
      const r = await slurm.createTenantUser({
        username: form.username.trim(),
        password: form.password,
        role: form.role,
      });
      setInfo(`已创建用户 ${r.user?.username || form.username.trim()}`);
      setForm(emptyUserForm);
      await refresh();
    } catch (err: any) {
      setError(`创建失败：${err?.message || err}`);
    } finally {
      setSubmitting(false);
    }
  };

  const rowBusy = (username: string) => acting.startsWith(`${username}:`);

  const toggleStatus = async (u: AdminUser) => {
    const next = (u.status || '').toLowerCase() === 'active' ? 'disabled' : 'active';
    setActing(`${u.username}:status`);
    setError('');
    setInfo('');
    try {
      await slurm.updateTenantUser(u.username, { status: next });
      setInfo(`用户 ${u.username} 已${next === 'active' ? '启用' : '禁用'}`);
      await refresh();
    } catch (e: any) {
      setError(`操作失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  const submitReset = async (username: string) => {
    if (!resetPw) {
      setError('请输入新密码');
      return;
    }
    setActing(`${username}:pw`);
    setError('');
    setInfo('');
    try {
      await slurm.resetTenantUserPassword(username, resetPw);
      setInfo(`已重置 ${username} 的密码`);
      setResetFor('');
      setResetPw('');
    } catch (e: any) {
      setError(`重置密码失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1rem' }}>租户用户管理</h2>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      <form
        onSubmit={submit}
        style={cardStyle}
      >
        <div style={{ fontSize: '1rem', fontWeight: 700, marginBottom: '0.75rem' }}>新建用户</div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(160px,1fr))', gap: '0.75rem' }}>
          <Field label="用户名">
            <input className="form-control" value={form.username} onChange={field('username')} placeholder="zhangsan" />
          </Field>
          <Field label="密码">
            <input className="form-control" type="password" value={form.password} onChange={field('password')} placeholder="初始密码" />
          </Field>
          <Field label="角色">
            <select
              className="form-control"
              value={form.role}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setForm({ ...form, role: e.target.value })}
            >
              <option value="member">member（普通成员）</option>
              <option value="tenant_admin">tenant_admin（租户管理员）</option>
            </select>
          </Field>
        </div>
        <button className="btn-primary" type="submit" disabled={submitting} style={{ justifySelf: 'start', padding: '0.5rem 1.5rem', marginTop: '0.75rem' }}>
          {submitting ? '创建中…' : '创建用户'}
        </button>
      </form>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', margin: '1.5rem 0 0.75rem' }}>
        <h3 style={{ margin: 0, fontSize: '1.05rem' }}>本租户用户</h3>
        <button className="btn-primary" onClick={refresh} style={{ padding: '0.3rem 0.9rem' }}>刷新</button>
      </div>

      <div className="table-card">
        {loading ? (
          <div style={emptyStyle}>加载中…</div>
        ) : users.length === 0 ? (
          <div style={emptyStyle}>暂无用户</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
              <thead>
                <tr style={{ textAlign: 'left' }}>
                  <th style={th}>用户名</th>
                  <th style={th}>角色</th>
                  <th style={th}>集群用户</th>
                  <th style={{ ...th, textAlign: 'right' }}>UID</th>
                  <th style={th}>状态</th>
                  <th style={th}>显示名</th>
                  <th style={th}>操作</th>
                </tr>
              </thead>
              <tbody>
                {users.map((u, i) => {
                  const isLast = i === users.length - 1;
                  const active = (u.status || '').toLowerCase() === 'active';
                  return (
                    <tr key={u.username} style={{ borderBottom: isLast ? 'none' : '1px solid var(--row-line,#2a2f3a)' }}>
                      <td style={td}><span style={{ fontWeight: 700 }}>{u.username}</span></td>
                      <td style={td}>{u.role || '-'}</td>
                      <td style={{ ...td, ...mono, color: 'var(--text-muted,#94a3b8)' }}>{u.clusterUser || '-'}</td>
                      <td style={{ ...td, ...num }}>{u.uid != null ? String(u.uid) : '-'}</td>
                      <td style={td}><StatusBadge status={u.status} /></td>
                      <td style={{ ...td, color: 'var(--text-muted,#94a3b8)' }}>{u.displayName || '-'}</td>
                      <td style={{ ...td, minWidth: 260 }}>
                        <div style={{ display: 'flex', gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
                          <MiniBtn disabled={rowBusy(u.username)} onClick={() => toggleStatus(u)}>
                            {active ? '禁用' : '启用'}
                          </MiniBtn>
                          <MiniBtn
                            disabled={rowBusy(u.username)}
                            onClick={() => {
                              setResetFor(resetFor === u.username ? '' : u.username);
                              setResetPw('');
                            }}
                          >
                            重置密码
                          </MiniBtn>
                          {resetFor === u.username && (
                            <>
                              <input
                                className="form-control"
                                type="password"
                                value={resetPw}
                                onChange={(e) => setResetPw(e.target.value)}
                                placeholder="新密码"
                                style={{ width: 140, padding: '0.3rem 0.6rem', fontSize: '0.8rem' }}
                              />
                              <MiniBtn disabled={acting === `${u.username}:pw`} onClick={() => submitReset(u.username)}>确认</MiniBtn>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

// ---------- admin：平台管理（租户 + 平台用户） ----------

const emptyTenantForm = { slug: '', name: '' };
const emptyPlatUserForm = { username: '', role: 'member', tenantSlug: '', password: '' };

// 全部四个角色（对齐后端 users.role CHECK 约束）
const PLATFORM_ROLES = [
  { value: 'member', label: 'member（普通成员）' },
  { value: 'tenant_admin', label: 'tenant_admin（租户管理员）' },
  { value: 'ops_admin', label: 'ops_admin（运维管理员）' },
  { value: 'admin', label: 'admin（平台管理员）' },
];

function PlatformAdminPanel() {
  const [tenants, setTenants] = useState<TenantInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [tenantForm, setTenantForm] = useState(emptyTenantForm);
  const [creatingTenant, setCreatingTenant] = useState(false);
  const [platUserForm, setPlatUserForm] = useState(emptyPlatUserForm);
  const [creatingUser, setCreatingUser] = useState(false);
  const [acting, setActing] = useState(''); // `tenant:<slug>`

  const refresh = useCallback(async () => {
    try {
      const r = await slurm.listTenants();
      setTenants(r.tenants || []);
      setError('');
    } catch (e: any) {
      setError(e?.message || '加载租户列表失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // 平台用户表单的租户下拉默认选中第一个租户
  useEffect(() => {
    if (!platUserForm.tenantSlug && tenants.length > 0) {
      setPlatUserForm((f) => ({ ...f, tenantSlug: tenants[0].slug }));
    }
  }, [tenants, platUserForm.tenantSlug]);

  const submitTenant = async (e: FormEvent) => {
    e.preventDefault();
    if (!tenantForm.slug.trim() || !tenantForm.name.trim()) {
      setError('租户标识与名称必填');
      return;
    }
    setCreatingTenant(true);
    setError('');
    setInfo('');
    try {
      const t = await slurm.createTenant(tenantForm.slug.trim(), tenantForm.name.trim());
      setInfo(`已创建租户 ${t?.slug || tenantForm.slug.trim()}`);
      setTenantForm(emptyTenantForm);
      await refresh();
    } catch (err: any) {
      setError(`创建租户失败：${err?.message || err}`);
    } finally {
      setCreatingTenant(false);
    }
  };

  const submitPlatUser = async (e: FormEvent) => {
    e.preventDefault();
    if (!platUserForm.username.trim() || !platUserForm.password || !platUserForm.tenantSlug) {
      setError('用户名、密码与所属租户必填');
      return;
    }
    setCreatingUser(true);
    setError('');
    setInfo('');
    try {
      const r = await slurm.createAdminUser({
        username: platUserForm.username.trim(),
        role: platUserForm.role,
        tenantSlug: platUserForm.tenantSlug,
        password: platUserForm.password,
      });
      setInfo(`已创建平台用户 ${r.user?.username || platUserForm.username.trim()}`);
      setPlatUserForm({ ...emptyPlatUserForm, tenantSlug: platUserForm.tenantSlug });
      await refresh();
    } catch (err: any) {
      setError(`创建用户失败：${err?.message || err}`);
    } finally {
      setCreatingUser(false);
    }
  };

  const toggleTenant = async (t: TenantInfo) => {
    const next = (t.status || '').toLowerCase() === 'active' ? 'suspended' : 'active';
    setActing(`tenant:${t.slug}`);
    setError('');
    setInfo('');
    try {
      await slurm.updateTenant(t.slug, { status: next });
      setInfo(`租户 ${t.slug} 已${next === 'active' ? '启用' : '停用'}`);
      await refresh();
    } catch (e: any) {
      setError(`操作失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };

  return (
    <div>
      <h2 style={{ marginTop: 0, marginBottom: '1rem' }}>平台管理</h2>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      <form onSubmit={submitTenant} style={cardStyle}>
        <div style={{ fontSize: '1rem', fontWeight: 700, marginBottom: '0.75rem' }}>新建租户</div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '0.75rem' }}>
          <Field label="租户标识（slug）">
            <input
              className="form-control"
              value={tenantForm.slug}
              onChange={(e) => setTenantForm({ ...tenantForm, slug: e.target.value })}
              placeholder="ai-group"
            />
          </Field>
          <Field label="租户名称">
            <input
              className="form-control"
              value={tenantForm.name}
              onChange={(e) => setTenantForm({ ...tenantForm, name: e.target.value })}
              placeholder="大模型分布式训练组"
            />
          </Field>
        </div>
        <button className="btn-primary" type="submit" disabled={creatingTenant} style={{ justifySelf: 'start', padding: '0.5rem 1.5rem', marginTop: '0.75rem' }}>
          {creatingTenant ? '创建中…' : '创建租户'}
        </button>
      </form>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', margin: '1.5rem 0 0.75rem' }}>
        <h3 style={{ margin: 0, fontSize: '1.05rem' }}>租户列表</h3>
        <button className="btn-primary" onClick={refresh} style={{ padding: '0.3rem 0.9rem' }}>刷新</button>
      </div>

      <div className="table-card">
        {loading ? (
          <div style={emptyStyle}>加载中…</div>
        ) : tenants.length === 0 ? (
          <div style={emptyStyle}>暂无租户</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
              <thead>
                <tr style={{ textAlign: 'left' }}>
                  <th style={th}>标识</th>
                  <th style={th}>名称</th>
                  <th style={th}>状态</th>
                  <th style={{ ...th, textAlign: 'right' }}>用户数</th>
                  <th style={th}>操作</th>
                </tr>
              </thead>
              <tbody>
                {tenants.map((t, i) => {
                  const isLast = i === tenants.length - 1;
                  const active = (t.status || '').toLowerCase() === 'active';
                  return (
                    <tr key={t.slug} style={{ borderBottom: isLast ? 'none' : '1px solid var(--row-line,#2a2f3a)' }}>
                      <td style={{ ...td, ...mono }}><span style={{ fontWeight: 700 }}>{t.slug}</span></td>
                      <td style={td}>
                        <div>{t.name || '-'}</div>
                        {t.parentAccount && (
                          <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)' }}>父账号 {t.parentAccount}</div>
                        )}
                      </td>
                      <td style={td}><StatusBadge status={t.status} /></td>
                      <td style={{ ...td, ...num }}>{t.userCount}</td>
                      <td style={td}>
                        <MiniBtn disabled={acting === `tenant:${t.slug}`} onClick={() => toggleTenant(t)}>
                          {active ? '停用' : '启用'}
                        </MiniBtn>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <form onSubmit={submitPlatUser} style={{ ...cardStyle, marginTop: '1.5rem' }}>
        <div style={{ fontSize: '1rem', fontWeight: 700, marginBottom: '0.75rem' }}>新建平台用户</div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '0.75rem' }}>
          <Field label="用户名">
            <input
              className="form-control"
              value={platUserForm.username}
              onChange={(e) => setPlatUserForm({ ...platUserForm, username: e.target.value })}
              placeholder="lisi"
            />
          </Field>
          <Field label="角色">
            <select
              className="form-control"
              value={platUserForm.role}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setPlatUserForm({ ...platUserForm, role: e.target.value })}
            >
              {PLATFORM_ROLES.map((r) => (
                <option key={r.value} value={r.value}>{r.label}</option>
              ))}
            </select>
          </Field>
          <Field label="所属租户">
            <select
              className="form-control"
              value={platUserForm.tenantSlug}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setPlatUserForm({ ...platUserForm, tenantSlug: e.target.value })}
            >
              {tenants.length === 0 ? (
                <option value="">（暂无租户）</option>
              ) : (
                tenants.map((t) => (
                  <option key={t.slug} value={t.slug}>{t.slug} · {t.name}</option>
                ))
              )}
            </select>
          </Field>
          <Field label="密码">
            <input
              className="form-control"
              type="password"
              value={platUserForm.password}
              onChange={(e) => setPlatUserForm({ ...platUserForm, password: e.target.value })}
              placeholder="初始密码"
            />
          </Field>
        </div>
        <button className="btn-primary" type="submit" disabled={creatingUser} style={{ justifySelf: 'start', padding: '0.5rem 1.5rem', marginTop: '0.75rem' }}>
          {creatingUser ? '创建中…' : '创建平台用户'}
        </button>
      </form>
    </div>
  );
}

// ---------- 共享小组件 / 样式（对齐 billing.tsx / jobs.tsx 既有模式） ----------

const cardStyle = {
  background: 'var(--bg-card,#1b1e28)',
  border: '1px solid var(--border-color,#2a2f3a)',
  borderRadius: 12,
  padding: '1.25rem',
  display: 'grid',
  gap: '0.75rem',
  boxShadow: 'var(--shadow-card)',
  transition: 'box-shadow .3s ease',
} as const;

const emptyStyle = { padding: '2.5rem', textAlign: 'center', color: 'var(--text-muted,#94a3b8)' } as const;

const th = {
  padding: '0.85rem 1.25rem',
  fontSize: '0.72rem',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  color: 'var(--text-muted,#94a3b8)',
  fontWeight: 700,
  borderBottom: '1px solid var(--border-color,#2a2f3a)',
} as const;

const td = {
  padding: '0.9rem 1.25rem',
  fontSize: '0.875rem',
  color: 'var(--text-main,#f1f5f9)',
} as const;

const mono = { fontFamily: "'JetBrains Mono', monospace" } as const;
const num = { ...mono, textAlign: 'right' } as const;

// 状态徽章配色：active=emerald / suspended=amber / disabled=rose（其余蓝灰兜底）
const STATUS_STYLES: Record<string, { color: string; bg: string }> = {
  active: { color: '#10b981', bg: 'rgba(16,185,129,.12)' },
  suspended: { color: '#f59e0b', bg: 'rgba(245,158,11,.12)' },
  disabled: { color: '#f43f5e', bg: 'rgba(244,63,94,.12)' },
};

function StatusBadge({ status }: { status: string }) {
  const s = STATUS_STYLES[(status || '').toLowerCase()] || { color: '#3b82f6', bg: 'rgba(59,130,246,.12)' };
  return (
    <span style={{ padding: '0.15rem 0.5rem', borderRadius: 6, fontSize: '0.72rem', fontWeight: 700, color: s.color, background: s.bg }}>
      {status || '-'}
    </span>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
      {label}
      {children}
    </label>
  );
}

function Notice({ color, bg, children }: { color: string; bg: string; children: ReactNode }) {
  return <div style={{ padding: '0.6rem 0.9rem', color, background: bg, borderRadius: 8, marginBottom: '1rem', fontSize: '0.88rem' }}>{children}</div>;
}

function MiniBtn({ disabled, onClick, children }: { disabled?: boolean; onClick: () => void; children: ReactNode }) {
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
