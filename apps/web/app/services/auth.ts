// 前端能力驱动（R4）：菜单/按钮/路由守卫只认权限点，不认角色名。
// 权限来源 = localStorage 'ails_user'.permissions（登录/refreshMe 写入）；
// 旧会话无 permissions 时回退内置角色映射（与后端 BuiltinRolePermissions 同步维护）。

import type { UserInfo } from './slurm';
import { slurm } from './slurm';

// 内置四角色 → 权限点（回退表；与 pkg/auth/permissions.go 的 BuiltinRolePermissions
// 保持一致——仅在旧 localStorage 缺 permissions 时使用，登录/刷新后即被服务端权威值取代）。
export const BUILTIN_ROLE_PERMISSIONS: Record<string, string[]> = {
  admin: [
    'cluster:read', 'nodes:manage',
    'tenants:read', 'tenants:manage', 'users:create', 'users:manage',
    'audit:read', 'reservations:manage', 'qos:manage', 'partitions:manage', 'roles:manage',
  ],
  ops_admin: ['cluster:read', 'billing:read'],
  tenant_admin: [
    'cluster:read', 'jobs:submit', 'jobs:control',
    'ide:list', 'ide:manage', 'billing:read',
    'tenant:users:read', 'tenant:users:manage', 'tenant:users:reset_password',
    'tenant:roles:manage',
  ],
  member: [
    'cluster:read', 'jobs:submit', 'jobs:control',
    'ide:list', 'ide:manage', 'billing:read',
  ],
};

export interface StoredUser {
  name: string;
  role: string;
  roleName?: string;
  permissions?: string[];
  org?: string;
  tenantNs?: string;
  clusterUser?: string;
}

export function getStoredUser(): StoredUser | null {
  const raw = localStorage.getItem('ails_user');
  if (!raw) return null;
  try {
    return JSON.parse(raw) as StoredUser;
  } catch {
    return null;
  }
}

export function permissionsOfUser(user: StoredUser | null): string[] {
  if (!user) return [];
  if (user.permissions && user.permissions.length > 0) return user.permissions;
  return BUILTIN_ROLE_PERMISSIONS[user.role] ?? [];
}

// can 判断当前登录者是否持有权限点（前端渲染门；服务端 RequirePermission 是权威门——
// 前端隐藏只是体验，绕过 UI 直调 API 仍会被 403）。
export function can(permission: string, user: StoredUser | null = getStoredUser()): boolean {
  return permissionsOfUser(user).includes(permission);
}

// refreshMe 拉取 /auth/me 并回写 localStorage（角色改派/权限调整后刷新页面即感知）。
// 静默失败（网络抖动/旧令牌）——保留旧值，由 401 拦截登录流程接管。
export async function refreshMe(): Promise<StoredUser | null> {
  const cur = getStoredUser();
  try {
    const data = await slurm.getMe();
    const stored: StoredUser = {
      name: data.user.username,
      role: data.user.role,
      roleName: data.user.roleName || undefined,
      permissions: data.user.permissions || [],
      org: data.user.orgSlug,
      tenantNs: data.user.tenantNs,
      clusterUser: data.user.clusterUser,
    };
    localStorage.setItem('ails_user', JSON.stringify(stored));
    return stored;
  } catch {
    return cur;
  }
}
