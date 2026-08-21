// RBAC 管理页（2026-08-19 IA 重组自 admin.tsx 拆出）：角色管理（平台/租户按权限）
// + 审计日志。防提权与服务端权限门不变，此处仅为信息架构归位。
import { createFileRoute } from '@tanstack/react-router';
import { useState, useEffect, type ChangeEvent } from 'react';
import { slurm, type AuditEntry } from '../services/slurm';
import { can, getStoredUser } from '../services/auth';
import { RolesPanel } from '../components/roles_panel';
import { Select } from '../components/select';
import { MiniBtn, cardStyle, emptyStyle, mono, th, td } from '../components/panel_ui';

export const Route = createFileRoute('/rbac')({ component: RbacPage });

const AUDIT_ACTIONS = [
  { value: 'auth.login', label: '登录成功' },
  { value: 'auth.login.fail', label: '登录失败' },
  { value: 'auth.login.locked', label: '登录锁定' },
  { value: 'user.create', label: '用户创建' },
  { value: 'user.update', label: '用户更新/禁用' },
  { value: 'user.reset_password', label: '密码重置' },
  { value: 'user.role', label: '角色改派' },
  { value: 'token.create', label: '令牌签发' },
  { value: 'token.revoke', label: '令牌吊销' },
  { value: 'role.create', label: '角色创建' },
  { value: 'role.update', label: '角色更新' },
  { value: 'role.delete', label: '角色删除' },
  { value: 'tenant.create', label: '租户创建' },
  { value: 'tenant.update', label: '租户更新' },
  { value: 'tenant.qos', label: '租户 QOS 绑定' },
  { value: 'reservations.create', label: '预约创建' },
  { value: 'reservations.delete', label: '预约删除' },
  { value: 'qos.create', label: 'QOS 创建' },
  { value: 'partition.update', label: '分区修改' },
  { value: 'nodes.state', label: '节点 DRAIN/RESUME' },
  { value: 'jobs.submit', label: '作业提交' },
  { value: 'jobs.cancel', label: '作业取消' },
  { value: 'ide.launch', label: 'IDE 启动' },
];

function RbacPage() {
  const user = getStoredUser();
  const showPlatformRoles = can('roles:manage', user);
  const showTenantRoles = can('tenant:roles:manage', user);
  const showAudit = can('audit:read', user);

  return (
    <div>
      {!showPlatformRoles && !showTenantRoles && !showAudit && (
        <div style={{ padding: '0.6rem 0.9rem', color: 'var(--accent-rose)', background: 'rgba(239,68,68,.1)', borderRadius: 8, fontSize: '0.88rem' }}>
          当前账号无 RBAC 管理权限。
        </div>
      )}
      {showTenantRoles && <RolesPanel scope="tenant" />}
      {showPlatformRoles && <RolesPanel scope="platform" />}
      {showAudit && <AuditPanel />}
    </div>
  );
}

function AuditPanel() {
  const [audit, setAudit] = useState<AuditEntry[]>([]);
  const [auditFilter, setAuditFilter] = useState('');
  const [auditAction, setAuditAction] = useState('');
  const [error, setError] = useState('');
  const loadAudit = async () => {
    try {
      const r = await slurm.listAudit(auditFilter.trim() || undefined, auditAction || undefined, 100);
      setAudit(r.entries || []);
      setError('');
    } catch (e: any) {
      setError(`审计读取失败：${e?.message || e}`);
    }
  };
  useEffect(() => {
    loadAudit();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div style={{ ...cardStyle, marginTop: '1.5rem', display: 'block' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: '0.75rem', marginBottom: '0.75rem' }}>
        <div style={{ fontSize: '1rem', fontWeight: 700 }}>审计日志（最近 100 条）</div>
        <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
          <input
            className="form-control form-control-sm"
            placeholder="按操作者过滤"
            value={auditFilter}
            onChange={(e) => setAuditFilter(e.target.value)}
            style={{ width: 150 }}
          />
          <Select
            small
            width={190}
            value={auditAction}
            onChange={setAuditAction}
            options={[{ value: '', label: '全部动作' }, ...AUDIT_ACTIONS.map((a) => ({ value: a.value, label: a.label }))]}
            ariaLabel="按动作过滤"
          />
          <MiniBtn onClick={loadAudit}>查询</MiniBtn>
        </div>
      </div>
      {error && <div style={{ padding: '0.5rem 0.7rem', background: 'rgba(239,68,68,.1)', color: 'var(--accent-rose)', borderRadius: 6, fontSize: '0.8rem', marginBottom: '0.8rem' }}>{error}</div>}
      {audit.length === 0 ? (
        <div style={emptyStyle}>无匹配记录</div>
      ) : (
        <div style={{ overflowX: 'auto', maxHeight: 420, overflowY: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.85rem' }}>
            <thead>
              <tr style={{ textAlign: 'left' }}>
                <th style={th}>时间</th><th style={th}>操作者</th><th style={th}>动作</th>
                <th style={th}>目标</th><th style={th}>详情</th>
              </tr>
            </thead>
            <tbody>
              {audit.map((e, i) => (
                <tr key={e.id ?? i} style={{ borderBottom: i === audit.length - 1 ? 'none' : '1px solid var(--row-line,#2a2f3a)' }}>
                  <td style={{ ...td, ...mono, fontSize: '0.75rem', color: 'var(--text-muted,#94a3b8)', whiteSpace: 'nowrap' }}>{e.createdAt || '-'}</td>
                  <td style={td}>{e.actor || '-'}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.78rem' }}>{e.action}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.78rem' }}>{e.target || '-'}</td>
                  <td style={{ ...td, ...mono, fontSize: '0.72rem', color: 'var(--text-muted,#94a3b8)', maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={e.detail}>{e.detail || '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
