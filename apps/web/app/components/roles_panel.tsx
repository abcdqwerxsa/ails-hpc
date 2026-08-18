// R3/R4 角色管理面板（平台与租户两用）。权限点复选框按调用者自身权限禁用越界项——
// 服务端 ensureSubset 才是权威（前端禁用仅体验层）。
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { slurm, type RoleInfo } from '../services/slurm';
import { can, permissionsOfUser, getStoredUser } from '../services/auth';

// 权限点中文名（与后端权威词汇表 pkg/auth/permissions.go 同步维护）
export const PERMISSION_LABELS: Record<string, string> = {
  'cluster:read': '集群状态查看',
  'nodes:manage': '节点 DRAIN/RESUME',
  'jobs:submit': '作业提交',
  'jobs:control': '作业控制(取消/挂起/重排)',
  'ide:list': 'IDE 会话列表',
  'ide:manage': 'IDE 启动/回收/延时',
  'billing:read': '计费读取',
  'tenants:read': '平台租户查看',
  'tenants:manage': '平台租户管理',
  'users:create': '平台用户创建',
  'audit:read': '审计日志查看',
  'reservations:manage': '预约管理',
  'qos:manage': 'QOS 管理',
  'roles:manage': '平台角色管理',
  'tenant:users:read': '本租户成员查看',
  'tenant:users:manage': '本租户成员管理',
  'tenant:users:reset_password': '本租户成员改密',
  'tenant:roles:manage': '本租户角色管理',
};

const ALL_PERMISSIONS = Object.keys(PERMISSION_LABELS);

interface RolesPanelProps {
  scope: 'platform' | 'tenant';
}

export function RolesPanel({ scope }: RolesPanelProps) {
  const isPlatform = scope === 'platform';
  const [roles, setRoles] = useState<RoleInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [form, setForm] = useState({ name: '', description: '', baseRole: isPlatform ? 'ops_admin' : 'member' });
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  const actorPerms = permissionsOfUser(getStoredUser());

  const refresh = useCallback(async () => {
    try {
      const r = isPlatform ? await slurm.listPlatformRoles() : await slurm.listMyRoles();
      setRoles(r.roles || []);
      setError('');
    } catch (e: any) {
      setError(e?.message || '加载角色失败');
    } finally {
      setLoading(false);
    }
  }, [isPlatform]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const toggle = (p: string) =>
    setSelected((cur) => (cur.includes(p) ? cur.filter((x) => x !== p) : [...cur, p]));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) {
      setError('角色名必填');
      return;
    }
    setBusy(true);
    setError('');
    setInfo('');
    try {
      const payload = {
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        permissions: selected,
        baseRole: form.baseRole,
      };
      const r = isPlatform ? await slurm.createPlatformRole(payload) : await slurm.createMyRole(payload);
      setInfo(`已创建角色 ${r.role?.name || form.name.trim()}（${selected.length} 项权限）`);
      setForm({ name: '', description: '', baseRole: isPlatform ? 'ops_admin' : 'member' });
      setSelected([]);
      await refresh();
    } catch (err: any) {
      setError(`创建失败：${err?.message || err}`);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (name: string) => {
    setError('');
    setInfo('');
    try {
      if (isPlatform) await slurm.deletePlatformRole(name);
      else await slurm.deleteMyRole(name);
      setInfo(`已删除角色 ${name}`);
      await refresh();
    } catch (err: any) {
      setError(`删除失败：${err?.message || err}`);
    }
  };

  return (
    <div style={{ marginTop: '1.5rem' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', margin: '0 0 0.75rem' }}>
        <h3 style={{ margin: 0, fontSize: '1.05rem' }}>{isPlatform ? '平台角色' : '本租户角色'}</h3>
        <button className="btn-primary" onClick={refresh} style={{ padding: '0.3rem 0.9rem' }}>刷新</button>
      </div>
      <p style={{ margin: '0 0 0.75rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' }}>
        自定义角色的权限只能是创建者自身权限的子集（服务端校验，越界项已置灰）。内置四角色不可删改。
      </p>

      {error && <Notice color="#f43f5e" bg="rgba(239,68,68,.1)">{error}</Notice>}
      {info && <Notice color="#10b981" bg="rgba(16,185,129,.1)">{info}</Notice>}

      <form onSubmit={submit} style={{ ...cardStyle, marginBottom: '1rem' }}>
        <div style={{ fontSize: '1rem', fontWeight: 700 }}>新建自定义角色</div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(180px,1fr))', gap: '0.75rem' }}>
          <label style={fieldStyle}>
            角色名
            <input className="form-control" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="senior-dev" />
          </label>
          <label style={fieldStyle}>
            描述
            <input className="form-control" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} placeholder="可选" />
          </label>
          <label style={fieldStyle}>
            基角色（数据范围）
            <select className="form-control" value={form.baseRole} onChange={(e) => setForm({ ...form, baseRole: e.target.value })}>
              {isPlatform ? (
                <>
                  <option value="ops_admin">ops_admin（平台全量可见）</option>
                  <option value="admin">admin（平台管理员）</option>
                </>
              ) : (
                <>
                  <option value="member">member（仅本人数据）</option>
                  <option value="tenant_admin">tenant_admin（本租户数据）</option>
                </>
              )}
            </select>
          </label>
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.5rem' }}>
          {ALL_PERMISSIONS.map((p) => {
            const allowed = actorPerms.includes(p);
            const on = selected.includes(p);
            return (
              <label
                key={p}
                title={allowed ? PERMISSION_LABELS[p] : `${PERMISSION_LABELS[p]}——超出你的权限，不可授予`}
                style={{
                  display: 'inline-flex', alignItems: 'center', gap: '0.35rem',
                  padding: '0.25rem 0.6rem', borderRadius: 8, fontSize: '0.78rem',
                  border: `1px solid ${on ? 'var(--accent-primary,#3b82f6)' : 'var(--border-color,#2a2f3a)'}`,
                  opacity: allowed ? 1 : 0.35,
                  cursor: allowed ? 'pointer' : 'not-allowed',
                  color: 'var(--text-main,#f1f5f9)',
                  background: on ? 'rgba(59,130,246,.12)' : 'transparent',
                }}
              >
                <input type="checkbox" checked={on && allowed} disabled={!allowed} onChange={() => toggle(p)} />
                {PERMISSION_LABELS[p]}
              </label>
            );
          })}
        </div>
        <button className="btn-primary" type="submit" disabled={busy} style={{ justifySelf: 'start', padding: '0.5rem 1.5rem' }}>
          {busy ? '创建中…' : '创建角色'}
        </button>
      </form>

      <div className="table-card">
        {loading ? (
          <div style={emptyStyle}>加载中…</div>
        ) : roles.length === 0 ? (
          <div style={emptyStyle}>暂无角色</div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
              <thead>
                <tr style={{ textAlign: 'left' }}>
                  <th style={th}>角色</th>
                  <th style={th}>类型</th>
                  <th style={th}>基角色</th>
                  <th style={th}>权限</th>
                  <th style={{ ...th, textAlign: 'right' }}>在用用户</th>
                  <th style={th}>操作</th>
                </tr>
              </thead>
              <tbody>
                {roles.map((r, i) => (
                  <tr key={r.id} style={{ borderBottom: i === roles.length - 1 ? 'none' : '1px solid var(--row-line,#2a2f3a)' }}>
                    <td style={{ ...td, ...mono }}>
                      <span style={{ fontWeight: 700 }}>{r.name}</span>
                      {r.description && <div style={{ fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)' }}>{r.description}</div>}
                    </td>
                    <td style={td}>{r.isSystem ? '内置' : '自定义'}</td>
                    <td style={td}>{r.baseRole}</td>
                    <td style={{ ...td, fontSize: '0.78rem' }}>
                      {(r.permissions || []).map((p) => (
                        <span key={p} title={p} style={{ display: 'inline-block', margin: '1px 4px 1px 0', padding: '0.1rem 0.45rem', borderRadius: 6, background: 'rgba(59,130,246,.1)', color: 'var(--text-muted,#94a3b8)' }}>
                          {PERMISSION_LABELS[p] || p}
                        </span>
                      ))}
                    </td>
                    <td style={{ ...td, ...num }}>{r.userCount}</td>
                    <td style={td}>
                      {!r.isSystem && (
                        <MiniBtn onClick={() => remove(r.name)}>删除</MiniBtn>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

// RoleAssignSelect 用户行内角色改派下拉（options = 内置可选 + 本作用域自定义角色）。
export function RoleAssignSelect({
  username,
  currentRole,
  currentRoleName,
  scope,
  onDone,
}: {
  username: string;
  currentRole: string;
  currentRoleName?: string;
  scope: 'platform' | 'tenant';
  onDone: (msg: string) => void;
}) {
  const [roles, setRoles] = useState<RoleInfo[]>([]);
  const [value, setValue] = useState(currentRoleName || currentRole);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const r = scope === 'platform' ? await slurm.listPlatformRoles() : await slurm.listMyRoles();
        setRoles((r.roles || []).filter((x) => !x.isSystem || x.name === currentRole));
      } catch {
        /* 角色清单拉取失败 → 仅内置选项 */
      }
    })();
  }, [scope, currentRole]);

  const apply = async () => {
    setBusy(true);
    try {
      if (scope === 'platform') await slurm.assignPlatformRole(username, value);
      else await slurm.assignMyRole(username, value);
      onDone(`用户 ${username} 已改派为角色 ${value}`);
    } catch (e: any) {
      onDone(`改派失败：${e?.message || e}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'inline-flex', gap: '0.35rem', alignItems: 'center' }}>
      <select
        className="form-control form-control-sm"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        style={{ width: 150 }}
      >
        {roles.map((r) => (
          <option key={r.id} value={r.name}>{r.name}</option>
        ))}
      </select>
      <MiniBtn disabled={busy || value === (currentRoleName || currentRole)} onClick={apply}>
        {busy ? '…' : '改派'}
      </MiniBtn>
    </div>
  );
}

// 与 admin.tsx 共享的样式（此处组件自成一体，避免循环依赖）
const cardStyle = {
  background: 'var(--bg-card,#1b1e28)',
  border: '1px solid var(--border-color,#2a2f3a)',
  borderRadius: 12,
  padding: '1.25rem',
  display: 'grid',
  gap: '0.75rem',
  boxShadow: 'var(--shadow-card)',
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
  verticalAlign: 'top',
} as const;

const mono = { fontFamily: "'JetBrains Mono', monospace" } as const;
const num = { ...mono, textAlign: 'right' } as const;
const fieldStyle = { display: 'grid', gap: '0.25rem', fontSize: '0.8rem', color: 'var(--text-muted,#94a3b8)' } as const;

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
      }}
    >
      {children}
    </button>
  );
}
