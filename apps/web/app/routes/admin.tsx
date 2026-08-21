import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useEffect, useState, type ChangeEvent, type FormEvent, type ReactNode } from 'react';
import { slurm, type AdminUser, type TenantInfo, type QOSInfo, type UserQOSUpdates } from '../services/slurm';
import { can, getStoredUser } from '../services/auth';
import { RoleAssignSelect, TenantMoveControl } from '../components/roles_panel';
import { Select } from '../components/select';
import { Field, MiniBtn, Notice, StatusBadge, cardStyle, emptyStyle, mono, num, th, td } from '../components/panel_ui';

export const Route = createFileRoute('/admin')({ component: AdminPage });

// R4 能力驱动：面板按权限点渲染（角色名硬编码已全部移除；服务端 RequirePermission 为
// 权威门）。持有对应权限即显示对应面板——自定义角色可获得单个面板（如仅审计）。
function AdminPage() {
  const user = getStoredUser();
  const showPlatform = can('tenants:read', user);
  const showTenant = can('tenant:users:read', user);
  if (!showPlatform && !showTenant) {
    return (
      <div>
        <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">当前账号无管理权限。</Notice>
      </div>
    );
  }
  return (
    <div>
      {showTenant && <TenantUsersPanel />}
      {showPlatform && <PlatformAdminPanel showTenantTopMargin={showTenant} />}
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
  const [qosUser, setQosUser] = useState<AdminUser | null>(null);

  const user = getStoredUser();
  const canManageUsers = can('tenant:users:manage', user);
  const canResetPw = can('tenant:users:reset_password', user);

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
      <h3 style={{ marginTop: 0, marginBottom: '1rem' }}>租户用户管理</h3>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      {canManageUsers && (
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
              <Select
                value={form.role}
                onChange={(v) => setForm({ ...form, role: v })}
                options={[
                  { value: 'member', label: 'member（普通成员）' },
                  { value: 'tenant_admin', label: 'tenant_admin（租户管理员）' },
                ]}
              />
            </Field>
          </div>
          <button className="btn-primary" type="submit" disabled={submitting} style={{ justifySelf: 'start', padding: '0.5rem 1.5rem', marginTop: '0.75rem' }}>
            {submitting ? '创建中…' : '创建用户'}
          </button>
        </form>
      )}

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
                  <th style={th}>QOS 策略</th>
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
                      <td style={td}>
                        <div>{(u as any).roleName || u.role || '-'}</div>
                        {(u as any).roleName && (u as any).roleName !== u.role && (
                          <div style={{ fontSize: '0.72rem', color: 'var(--text-muted,#94a3b8)' }}>基角色 {u.role}</div>
                        )}
                      </td>
                      <td style={{ ...td, ...mono, color: 'var(--text-muted,#94a3b8)' }}>{u.clusterUser || '-'}</td>
                      <td style={{ ...td, ...num }}>{u.uid != null ? String(u.uid) : '-'}</td>
                      <td style={td}><StatusBadge status={u.status} /></td>
                      <td style={{ ...td, color: 'var(--text-muted,#94a3b8)' }}>{u.displayName || '-'}</td>
                      <td style={td}>
                        <UserQOSCell username={u.username} />
                      </td>
                      <td style={{ ...td, minWidth: 280 }}>
                        <div style={{ display: 'flex', gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
                          {canManageUsers && (
                            <MiniBtn onClick={() => setQosUser(u)}>
                              QOS 设置
                            </MiniBtn>
                          )}
                          {canManageUsers && (
                            <MiniBtn disabled={rowBusy(u.username)} onClick={() => toggleStatus(u)}>
                              {active ? '禁用' : '启用'}
                            </MiniBtn>
                          )}
                          {canResetPw && (
                            <MiniBtn
                              disabled={rowBusy(u.username)}
                              onClick={() => {
                                setResetFor(resetFor === u.username ? '' : u.username);
                                setResetPw('');
                              }}
                            >
                              重置密码
                            </MiniBtn>
                          )}
                          {resetFor === u.username && (
                            <>
                              <input
                                className="form-control form-control-sm"
                                type="password"
                                value={resetPw}
                                onChange={(e) => setResetPw(e.target.value)}
                                placeholder="新密码"
                                style={{ width: 140 }}
                              />
                              <MiniBtn disabled={acting === `${u.username}:pw`} onClick={() => submitReset(u.username)}>确认</MiniBtn>
                            </>
                          )}
                          {canManageUsers && (
                            <RoleAssignSelect
                              username={u.username}
                              currentRole={u.role || 'member'}
                              currentRoleName={(u as any).roleName}
                              scope="tenant"
                              onDone={(m) => {
                                setInfo(m);
                                refresh();
                              }}
                            />
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

      {qosUser && (
        <UserQOSModal
          username={qosUser.username}
          clusterUser={qosUser.clusterUser}
          tenantSlug={qosUser.tenantSlug}
          scope="tenant"
          onClose={() => setQosUser(null)}
          onSuccess={(m) => {
            setInfo(m);
            setQosUser(null);
            refresh();
          }}
        />
      )}
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

function PlatformAdminPanel({ showTenantTopMargin }: { showTenantTopMargin?: boolean }) {
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

  const setLimit = async (t: TenantInfo) => {
    const cur = prompt(`设置租户 ${t.slug} 的 GrpTRES 限额（Slurm 语法，如 cpu=4,mem=8G；留空取消）`, '');
    if (!cur || !cur.trim()) return;
    setActing(`tenant:${t.slug}`);
    setError('');
    setInfo('');
    try {
      await slurm.updateTenant(t.slug, { grpTRES: cur.trim() });
      setInfo(`租户 ${t.slug} 限额已设为 ${cur.trim()}（Slurm 生效）`);
    } catch (e: any) {
      setError(`限额设置失败：${e?.message || e}`);
    } finally {
      setActing('');
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

  // v3-U 平台用户目录（users:manage）：全量拉取 + 前端过滤（规模几十~几百）；
  // 操作=禁用/启用、重置密码（内联）、显示名编辑、角色改派（roles:manage 门）
  const canManageUsers = can('users:manage', getStoredUser());
  const canAssignRoles = can('roles:manage', getStoredUser());
  const selfName = getStoredUser()?.name;
  const [dirUsers, setDirUsers] = useState<AdminUser[]>([]);
  const [dirLoading, setDirLoading] = useState(true);
  const [dirTenant, setDirTenant] = useState('');
  const [dirQ, setDirQ] = useState('');
  const [userResetFor, setUserResetFor] = useState('');
  const [userResetPw, setUserResetPw] = useState('');
  const [platQosUser, setPlatQosUser] = useState<AdminUser | null>(null);
  const loadDir = async () => {
    try {
      const r = await slurm.listPlatformUsers();
      setDirUsers(r.users || []);
      setError('');
    } catch (e: any) {
      setError(`用户目录读取失败：${e?.message || e}`);
    } finally {
      setDirLoading(false);
    }
  };
  const toggleUser = async (u: AdminUser) => {
    const next = (u.status || '').toLowerCase() === 'active' ? 'disabled' : 'active';
    if (next === 'disabled' && !confirm(`禁用用户 ${u.username}？其全部在途会话将即刻失效。`)) return;
    setActing(`user:${u.username}`);
    setError('');
    setInfo('');
    try {
      await slurm.updatePlatformUser(u.username, { status: next });
      setInfo(`用户 ${u.username} 已${next === 'active' ? '启用' : '禁用'}`);
      await loadDir();
    } catch (e: any) {
      setError(`操作失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };
  const submitUserReset = async (username: string) => {
    if (!userResetPw) {
      setError('新密码不能为空');
      return;
    }
    setActing(`${username}:pw`);
    setError('');
    setInfo('');
    try {
      await slurm.resetPlatformUserPassword(username, userResetPw);
      setInfo(`用户 ${username} 密码已重置（首登强制改密）`);
      setUserResetFor('');
      setUserResetPw('');
    } catch (e: any) {
      setError(`重置失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };
  const editDisplayName = async (u: AdminUser) => {
    const v = prompt(`设置 ${u.username} 的显示名（当前：${u.displayName || '未设置'}）`, u.displayName || '');
    if (v === null || !v.trim()) return;
    const name = v.trim().slice(0, 64);
    setActing(`user:${u.username}`);
    setError('');
    setInfo('');
    try {
      await slurm.updatePlatformUser(u.username, { displayName: name });
      setInfo(`用户 ${u.username} 显示名已更新`);
      await loadDir();
    } catch (e: any) {
      setError(`显示名更新失败：${e?.message || e}`);
    } finally {
      setActing('');
    }
  };
  const dirShown = dirUsers.filter((u) => {
    const q = dirQ.trim().toLowerCase();
    return (
      (!dirTenant || u.tenantSlug === dirTenant) &&
      (!q ||
        u.username.toLowerCase().includes(q) ||
        (u.displayName || '').toLowerCase().includes(q))
    );
  });

  useEffect(() => {
    if (canManageUsers) loadDir();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div style={showTenantTopMargin ? { marginTop: '2rem' } : undefined}>
      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      {can('tenants:manage', getStoredUser()) && (
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
      )}

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
                        <div style={{ display: 'flex', gap: '0.4rem' }}>
                          {can('tenants:manage', getStoredUser()) && (
                            <>
                              <MiniBtn disabled={acting === `tenant:${t.slug}`} onClick={() => toggleTenant(t)}>
                                {active ? '停用' : '启用'}
                              </MiniBtn>
                              <MiniBtn disabled={acting === `tenant:${t.slug}`} onClick={() => setLimit(t)}>
                                限额
                              </MiniBtn>
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

      {canManageUsers && (
      <div className="table-card" style={{ marginTop: '1.5rem' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '0.75rem', marginBottom: '0.75rem' }}>
          <div style={{ fontSize: '1rem', fontWeight: 700 }}>平台用户目录</div>
          <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
            <Select
              small
              width={160}
              value={dirTenant}
              onChange={setDirTenant}
              options={[{ value: '', label: '全部租户' }, ...tenants.map((t) => ({ value: t.slug, label: t.slug }))]}
              ariaLabel="按租户过滤"
            />
            <input
              className="form-control form-control-sm"
              placeholder="搜索用户名/显示名"
              value={dirQ}
              onChange={(e) => setDirQ(e.target.value)}
              style={{ width: 180 }}
            />
          </div>
        </div>
        {dirLoading ? (
          <div style={emptyStyle}>加载中…</div>
        ) : dirShown.length === 0 ? (
          <div style={emptyStyle}>无匹配用户</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
              <thead>
                <tr style={{ textAlign: 'left' }}>
                  <th style={th}>用户名</th>
                  <th style={th}>角色</th>
                  <th style={th}>租户</th>
                  <th style={th}>集群用户</th>
                  <th style={th}>状态</th>
                  <th style={th}>显示名</th>
                  <th style={th}>QOS 策略</th>
                  <th style={th}>操作</th>
                </tr>
              </thead>
              <tbody>
                {dirShown.map((u, i) => {
                  const isLast = i === dirShown.length - 1;
                  const active = (u.status || '').toLowerCase() === 'active';
                  const isSelf = u.username === selfName;
                  return (
                    <tr key={u.username} style={{ borderBottom: isLast ? 'none' : '1px solid var(--row-line,#2a2f3a)' }}>
                      <td style={td}>
                        <span style={{ fontWeight: 700 }}>{u.username}</span>
                        {isSelf && <span style={{ marginLeft: '0.4rem', fontSize: '0.72rem', color: 'var(--accent-cyan,#06B6D4)' }}>（我）</span>}
                      </td>
                      <td style={td}>
                        <div>{u.roleName || u.role || '-'}</div>
                        {u.roleName && u.roleName !== u.role && (
                          <div style={{ fontSize: '0.72rem', color: 'var(--text-muted,#94a3b8)' }}>基角色 {u.role}</div>
                        )}
                      </td>
                      <td style={{ ...td, ...mono, color: 'var(--text-muted,#94a3b8)' }}>{u.tenantSlug || '-'}</td>
                      <td style={{ ...td, ...mono, color: 'var(--text-muted,#94a3b8)' }}>{u.clusterUser || '-'}</td>
                      <td style={td}><StatusBadge status={u.status} /></td>
                      <td style={{ ...td, color: 'var(--text-muted,#94a3b8)' }}>{u.displayName || '-'}</td>
                      <td style={td}>
                        <UserQOSCell username={u.username} />
                      </td>
                      <td style={{ ...td, minWidth: 280 }}>
                        <div style={{ display: 'flex', gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
                          {canManageUsers && (
                            <MiniBtn onClick={() => setPlatQosUser(u)}>
                              QOS 设置
                            </MiniBtn>
                          )}
                          <MiniBtn
                            disabled={acting === `user:${u.username}` || (isSelf && active)}
                            onClick={() => toggleUser(u)}
                          >
                            {active ? '禁用' : '启用'}
                          </MiniBtn>
                          <MiniBtn
                            disabled={acting === `user:${u.username}`}
                            onClick={() => {
                              setUserResetFor(userResetFor === u.username ? '' : u.username);
                              setUserResetPw('');
                            }}
                          >
                            重置密码
                          </MiniBtn>
                          {userResetFor === u.username && (
                            <>
                              <input
                                className="form-control form-control-sm"
                                type="password"
                                value={userResetPw}
                                onChange={(e) => setUserResetPw(e.target.value)}
                                placeholder="新密码"
                                style={{ width: 140 }}
                              />
                              <MiniBtn disabled={acting === `${u.username}:pw`} onClick={() => submitUserReset(u.username)}>确认</MiniBtn>
                            </>
                          )}
                          <MiniBtn disabled={acting === `user:${u.username}`} onClick={() => editDisplayName(u)}>
                            显示名
                          </MiniBtn>
                          {canAssignRoles && (
                            <TenantMoveControl
                              username={u.username}
                              currentTenant={u.tenantSlug || ''}
                              currentRole={u.role || 'member'}
                              onDone={(m) => {
                                setInfo(m);
                                loadDir();
                              }}
                            />
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
      )}

      {platQosUser && (
        <UserQOSModal
          username={platQosUser.username}
          clusterUser={platQosUser.clusterUser}
          tenantSlug={platQosUser.tenantSlug}
          scope="platform"
          onClose={() => setPlatQosUser(null)}
          onSuccess={(m) => {
            setInfo(m);
            setPlatQosUser(null);
            loadDir();
          }}
        />
      )}

      {can('users:create', getStoredUser()) && (
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
            <Select
              value={platUserForm.role}
              onChange={(v) => setPlatUserForm({ ...platUserForm, role: v })}
              options={PLATFORM_ROLES.map((r) => ({ value: r.value, label: r.label }))}
            />
          </Field>
          <Field label="所属租户">
            <Select
              value={platUserForm.tenantSlug}
              onChange={(v) => setPlatUserForm({ ...platUserForm, tenantSlug: v })}
              options={tenants.length === 0
                ? [{ value: '', label: '（暂无租户）' }]
                : tenants.map((t) => ({ value: t.slug, label: `${t.slug} · ${t.name}` }))}
            />
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
      )}

    </div>
  );
}

// ----------------------------------------------------
// QOS 用户关联与状态组件 (QOSBadge / UserQOSCell / UserQOSModal)
// ----------------------------------------------------

export function QOSBadge({ qos }: { qos?: string }) {
  const name = (qos || 'normal').toLowerCase();
  let bg = 'rgba(6, 182, 212, 0.12)';
  let color = 'var(--accent-cyan,#06b6d4)';
  let border = 'rgba(6, 182, 212, 0.25)';

  if (name.includes('vip') || name.includes('high') || name.includes('prio')) {
    bg = 'rgba(245, 158, 11, 0.15)';
    color = '#f59e0b';
    border = 'rgba(245, 158, 11, 0.3)';
  } else if (name.includes('debug') || name.includes('test')) {
    bg = 'rgba(16, 185, 129, 0.15)';
    color = '#34d399';
    border = 'rgba(16, 185, 129, 0.3)';
  } else if (name.includes('gpu')) {
    bg = 'rgba(168, 85, 247, 0.15)';
    color = '#c084fc';
    border = 'rgba(168, 85, 247, 0.3)';
  }

  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.3rem',
        padding: '0.15rem 0.5rem',
        borderRadius: 6,
        fontSize: '0.75rem',
        fontFamily: "'JetBrains Mono', monospace",
        fontWeight: 700,
        background: bg,
        color,
        border: `1px solid ${border}`,
      }}
    >
      <span style={{ width: 6, height: 6, borderRadius: '50%', background: color }} />
      {qos || 'normal'}
    </span>
  );
}

export function UserQOSCell({ username }: { username: string }) {
  const [qos, setQos] = useState<string>('normal');
  const [allowed, setAllowed] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    slurm
      .getUserQOS(username)
      .then((r) => {
        if (!active) return;
        const uQos = r.qos || r;
        const def = uQos.defaultQos || uQos.defaultQOS || (r as any).defaultQos || (r as any).defaultQOS || 'normal';
        const all = (uQos.allowedQos && uQos.allowedQos.length > 0)
          ? uQos.allowedQos
          : (uQos.allowedQOS && uQos.allowedQOS.length > 0)
          ? uQos.allowedQOS
          : (r as any).allowedQos || (r as any).allowedQOS || [def];
        setQos(def);
        setAllowed(all);
      })
      .catch(() => {
        if (active) {
          setQos('normal');
          setAllowed(['normal']);
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [username]);

  if (loading) {
    return <span style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)' }}>…</span>;
  }

  const otherAllowed = allowed.filter((a) => a !== qos);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.2rem' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.35rem' }}>
        <QOSBadge qos={qos} />
      </div>
      {otherAllowed.length > 0 && (
        <div style={{ fontSize: '0.7rem', color: 'var(--text-muted,#94a3b8)', fontFamily: "'JetBrains Mono', monospace" }}>
          + {otherAllowed.join(', ')}
        </div>
      )}
    </div>
  );
}

interface UserQOSModalProps {
  username: string;
  clusterUser?: string;
  tenantSlug?: string;
  scope: 'platform' | 'tenant';
  onClose: () => void;
  onSuccess: (msg: string) => void;
}

export function UserQOSModal({
  username,
  clusterUser,
  tenantSlug,
  scope,
  onClose,
  onSuccess,
}: UserQOSModalProps) {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [allQos, setAllQos] = useState<QOSInfo[]>([]);
  const [allowedQos, setAllowedQos] = useState<string[]>(['normal']);
  const [defaultQos, setDefaultQos] = useState<string>('normal');

  useEffect(() => {
    let active = true;
    (async () => {
      try {
        setLoading(true);
        const [qosRes, userQosRes] = await Promise.all([
          slurm.listQOS(),
          slurm.getUserQOS(username).catch(() => null),
        ]);
        if (!active) return;
        setAllQos(qosRes.qos || []);
        if (userQosRes) {
          const uQos = userQosRes.qos || userQosRes;
          const allowed = (uQos.allowedQos && uQos.allowedQos.length > 0)
            ? uQos.allowedQos
            : (uQos.allowedQOS && uQos.allowedQOS.length > 0)
            ? uQos.allowedQOS
            : (userQosRes as any).allowedQos || (userQosRes as any).allowedQOS || ['normal'];
          setAllowedQos(allowed);
          const def = uQos.defaultQos || uQos.defaultQOS || (userQosRes as any).defaultQos || (userQosRes as any).defaultQOS || allowed[0] || 'normal';
          setDefaultQos(def);
        }
      } catch (e: any) {
        if (active) setError(e?.message || '加载用户 QOS 数据失败');
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => {
      active = false;
    };
  }, [username]);

  const toggleQos = (name: string) => {
    let next: string[];
    if (allowedQos.includes(name)) {
      if (allowedQos.length <= 1) return; // 至少保留一个
      next = allowedQos.filter((q) => q !== name);
      if (defaultQos === name) {
        setDefaultQos(next[0] || 'normal');
      }
    } else {
      next = [...allowedQos, name];
    }
    setAllowedQos(next);
  };

  const handleSave = async () => {
    if (allowedQos.length === 0) {
      setError('至少需要保留一个允许使用的 QOS');
      return;
    }
    if (!allowedQos.includes(defaultQos)) {
      setError('默认 QOS 必须包含在允许的 QOS 清单中');
      return;
    }
    setSaving(true);
    setError('');
    try {
      const payload: UserQOSUpdates = {
        defaultQos,
        allowedQos,
      };
      if (scope === 'tenant') {
        await slurm.setTenantUserQOS(username, payload);
      } else {
        await slurm.setUserQOS(username, payload);
      }
      onSuccess(`用户 ${username} QOS 关联已更新（默认: ${defaultQos}，允许: ${allowedQos.join(', ')}）`);
      onClose();
    } catch (e: any) {
      setError(e?.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.65)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1100,
        padding: '1.5rem',
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="neu-chiseled-card"
        style={{ maxWidth: 640, width: '100%', maxHeight: '90vh', overflowY: 'auto', padding: '1.75rem' }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.25rem' }}>
          <div>
            <h3 style={{ margin: 0, fontSize: '1.15rem', fontWeight: 700 }}>
              用户 QOS 配额与调度治理
            </h3>
            <div style={{ fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)', marginTop: '0.25rem' }}>
              用户：<strong style={{ color: 'var(--text-main,#f1f5f9)' }}>{username}</strong> {clusterUser && `(${clusterUser})`} · 租户：<code>{tenantSlug || 'system'}</code>
            </div>
          </div>
          <MiniBtn onClick={onClose}>关闭</MiniBtn>
        </div>

        {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.12)">{error}</Notice>}

        {loading ? (
          <div style={{ padding: '2rem', textAlign: 'center', color: 'var(--text-muted,#94a3b8)' }}>加载配置中…</div>
        ) : (
          <div style={{ display: 'grid', gap: '1.25rem' }}>
            {/* 快捷模板预设 */}
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
              <span style={{ fontSize: '0.78rem', color: 'var(--text-muted,#94a3b8)', fontWeight: 600 }}>快捷预设：</span>
              <button
                type="button"
                className="neu-btn"
                style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}
                onClick={() => {
                  setAllowedQos(['normal']);
                  setDefaultQos('normal');
                }}
              >
                标准成员 (normal)
              </button>
              <button
                type="button"
                className="neu-btn"
                style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}
                onClick={() => {
                  const hasVip = allQos.some((q) => q.name === 'vip');
                  const allowed = hasVip ? ['normal', 'vip'] : ['normal'];
                  setAllowedQos(allowed);
                  setDefaultQos(hasVip ? 'vip' : 'normal');
                }}
              >
                科研骨干 (normal + vip)
              </button>
              <button
                type="button"
                className="neu-btn"
                style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}
                onClick={() => {
                  const names = allQos.map((q) => q.name);
                  setAllowedQos(names.length ? names : ['normal']);
                  setDefaultQos(names.includes('vip') ? 'vip' : names[0] || 'normal');
                }}
              >
                全权限授予
              </button>
            </div>

            {/* 允许使用的 QOS 复选框列表 */}
            <div>
              <div style={{ fontSize: '0.85rem', fontWeight: 700, marginBottom: '0.5rem', color: 'var(--text-main,#f1f5f9)' }}>
                允许使用的 QOS 清单 (Allowed QOS)
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: '0.65rem' }}>
                {allQos.map((q) => {
                  const checked = allowedQos.includes(q.name);
                  const isDefault = defaultQos === q.name;
                  return (
                    <div
                      key={q.name}
                      onClick={() => toggleQos(q.name)}
                      style={{
                        padding: '0.75rem 0.9rem',
                        borderRadius: 10,
                        cursor: 'pointer',
                        background: checked ? 'rgba(6, 182, 212, 0.08)' : 'var(--card-bg)',
                        border: checked ? '1px solid var(--accent-cyan,#06b6d4)' : '1px solid var(--border-color,#2a2f3a)',
                        display: 'grid',
                        gap: '0.35rem',
                        transition: 'all .2s ease',
                      }}
                    >
                      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={() => {}} // 由外层 div 驱动
                            style={{ cursor: 'pointer' }}
                          />
                          <span style={{ fontWeight: 700, fontFamily: "'JetBrains Mono', monospace", fontSize: '0.9rem' }}>{q.name}</span>
                        </div>
                        {isDefault && (
                          <span style={{ fontSize: '0.7rem', padding: '0.1rem 0.4rem', borderRadius: 4, background: 'var(--accent-cyan,#06b6d4)', color: '#fff', fontWeight: 700 }}>
                            当前默认
                          </span>
                        )}
                      </div>
                      <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)', display: 'flex', gap: '0.6rem', flexWrap: 'wrap' }}>
                        <span>优先级: {q.priority || '0'}</span>
                        {(q.max_wall || q.max_wall_duration) && <span>限时: {q.max_wall_duration || q.max_wall}</span>}
                        {(q.max_tres_per_user || q.max_tres) && <span>单人: {q.max_tres_per_user || q.max_tres}</span>}
                        {(q.max_jobs_per_user || q.max_jobs) && <span>并发: {q.max_jobs_per_user || q.max_jobs}</span>}
                      </div>
                      {q.description && (
                        <div style={{ fontSize: '0.72rem', color: 'var(--text-dim,#64748b)', fontStyle: 'italic' }}>
                          {q.description}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>

            {/* 默认 QOS 下拉选择 */}
            <div>
              <div style={{ fontSize: '0.85rem', fontWeight: 700, marginBottom: '0.4rem', color: 'var(--text-main,#f1f5f9)' }}>
                默认 QOS (Default QOS)
              </div>
              <Select
                value={defaultQos}
                onChange={setDefaultQos}
                options={allowedQos.map((name) => {
                  const q = allQos.find((x) => x.name === name);
                  return {
                    value: name,
                    label: `${name}${q?.description ? ` (${q.description})` : ''}${q?.priority ? ` · 优先级 ${q.priority}` : ''}`,
                  };
                })}
              />
              <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)', marginTop: '0.35rem' }}>
                用户在提交作业或启动 IDE 未显式指定 QOS 时，将自动套用该默认 QOS。
              </div>
            </div>

            {/* 底部按钮 */}
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem', marginTop: '0.5rem' }}>
              <button type="button" className="neu-btn" onClick={onClose} disabled={saving}>
                取消
              </button>
              <button type="button" className="btn-primary" onClick={handleSave} disabled={saving} style={{ padding: '0.5rem 1.5rem' }}>
                {saving ? '保存中…' : '保存 QOS 配置'}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}


