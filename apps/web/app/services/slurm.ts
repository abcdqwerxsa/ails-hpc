// Slurm apiserver（Go，:8090）的 typed API 客户端。
// JWT 由 login 写入 localStorage('ails_token')，每个请求自动附 Authorization 头。
// 错误体走统一信封 {error, request_id?, ...extras}；非 2xx 抛 ApiError(message=error)。
//
// 注：API_BASE 当前指向生产 apiserver（与 login 一致）。阶段 5（gin 服务 React 构建产物、
// 同源）后改为相对 "/api/v1" 即可。
// dev（vite:3000）直连生产 apiserver（CORS 允许）；prod（gin 服务 React，同源）用相对路径。
const API_BASE = import.meta.env.DEV ? "http://192.168.20.226:8090/api/v1" : "/api/v1";

export function getToken(): string {
  return localStorage.getItem("ails_token") || "";
}

export class ApiError extends Error {
  constructor(
    message: string,
    public requestId?: string,
    public status?: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...((options.headers as Record<string, string>) || {}),
  };
  const token = getToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}${path}`, { ...options, headers });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const errMsg = (body && typeof body === "object" && "error" in body ? String((body as any).error) : "") || `HTTP ${res.status}`;
    throw new ApiError(errMsg, (body as any)?.request_id, res.status);
  }
  return body as T;
}

// --- 类型（字段对齐 Go 后端 types） ---
export interface UserInfo {
  username: string;
  role: string;
  orgSlug: string;
  tenantNs: string;
  clusterUser?: string;
  account?: string;
  tenantSlug?: string;
}
export interface LoginResponse {
  token: string;
  user: UserInfo;
}

export interface NodeStateInfo {
  name: string;
  state: string; // IDLE | ALLOCATED | MIXED | DRAIN | DOWN | ...
  cpus: number;
  alloc_cpus: number;
  real_memory: number;
  alloc_memory: number;
  cores: number;
  gpus: number; // 配置 GPU 数（来自 gres gpu:N）
  alloc_gpus: number; // 已占用 GPU 数
  reason?: string;
}
export interface NodesListResponse {
  nodes: NodeStateInfo[];
}
export interface NodeStateUpdateResponse {
  node_name: string;
  state: string;
  message: string;
}

export interface PingResponse {
  meta?: { Slurm?: { release?: string } };
  errors?: unknown[];
  pings?: { hostname: string; ping: string; status: number; mode: string }[];
}

// 作业相关类型（字段对齐 pkg/services/jobs/types.go）
export interface JobSummary {
  job_id: number;
  name: string;
  partition: string;
  job_state: string;
  nodes: string;
  time_limit: number;
  submit_time: number;
  owner?: string; // 提交者 clusterUser（slurm account 回填）
}
export interface JobDetail {
  job_id: number;
  name: string;
  owner?: string;
  account?: string;
  partition?: string;
  state: string;
  elapsed_sec?: number;
  exit_code?: string;
  start?: string;
  end?: string;
  submit?: string;
  stdout_tail?: string;
}
export interface HistoryEntry {
  job_id: number;
  name: string;
  owner?: string;
  account?: string;
  partition?: string;
  state: string;
  elapsed_sec?: number;
  exit_code?: string;
  submit?: string;
  start?: string;
  end?: string;
}
export interface HistoryResponse {
  history: HistoryEntry[];
}
export interface JobListResponse {
  code?: number;
  jobs: JobSummary[];
}
export interface SubmitJobRequest {
  name: string;
  partition: string;
  memory_mb?: number; // 显式内存 MB（缺省=350MB/核）
  gpus?: number; // GPU 卡数（仅 performance 分区，1.1）
  nodes?: number;
  tasks?: number;
  cpus?: number;
  cpus_per_task?: number;
  time_limit: string; // 后端 FlexTimeLimit 兼容 string|int
  script: string;
  current_working_directory?: string;
}
export interface SubmitJobResponse {
  code: number;
  message: string;
  job_id: number;
  name?: string;
  status?: string;
}
export interface JobControlResponse {
  code: number;
  message: string;
  job_id: number;
  action: string;
  status?: string;
}

// Web-IDE 会话相关类型（字段对齐 pkg/services/containers/types.go）
export interface ContainerInstance {
  container_id: string;
  env_type: string; // jupyter | vscode
  status: string; // STARTING | RUNNING | STOPPED
  web_url: string; // /api/v1/ide/<session>/
  job_id: number;
  node?: string;
  nodes: number;
  cpus: number;
  memory_mb: number;
  created_at: string;
}
export interface ContainerListResponse {
  containers: ContainerInstance[];
}
export interface ContainerLaunchRequest {
  env_type: string; // jupyter | vscode
  nodes?: number;
  cpus?: number;
  memory_mb?: number;
}
export interface ContainerLaunchResponse {
  container_id: string;
  env_type: string;
  status: string;
  web_url: string;
  allocated?: ContainerInstance;
}
export interface ContainerRecycleResponse {
  container_id: string;
  status: string;
  message: string;
}

// 分区（阶段 4）
export interface Partition {
  name: string;
  nodes: string;
  total_cpus: number;
  total_nodes: number;
  total_memory_mb?: number; // 分区 TRES mem
  gpus?: number; // 成员节点 GRES 聚合
  alloc_gpus?: number;
}
export interface PartitionsResponse {
  errors?: unknown[];
  partitions: Partition[];
}

export interface MonitorResource {
  alloc: number;
  total: number;
}
export interface MonitorDisk {
  used_kb: number;
  total_kb: number;
  percent: number;
}
export interface MonitorSnapshot {
  cpu: MonitorResource;
  mem: MonitorResource;
  gpu: MonitorResource;
  disk: MonitorDisk;
}

// 监控趋势持久化历史（服务端累积，0-100 百分比，旧→新，≤360 点）
export interface MonitorHistory {
  timestamps: number[];
  cpu: number[];
  mem: number[];
  gpu: number[];
  disk: number[];
}

// 计费（阶段 4）
export interface BreakdownRow {
  user: string;
  account: string;
  cpu_hours: number;
  mem_gb_hours: number;
  gpu_hours: number;
  job_count: number;
}
export interface BillingUsage {
  user: string;
  project: string;
  total_cpu_hours: number;
  total_memory_gb_hours: number;
  total_gpu_hours: number;
  job_count: number;
  container_count: number;
  breakdown?: BreakdownRow[];
}
export interface BillingExportJSON {
  format: string;
  user: string;
  timestamp: string;
  total_cost: number;
  currency: string;
  job_count: number;
  ctr_count: number;
  exported_by: string;
}

// 多租户管理（阶段 3）。注：与 jobs/billing 的 snake_case 不同，
// 本组接口字段对齐后端 tenant/user handler 的 camelCase 序列化。
export interface TenantInfo {
  slug: string;
  name: string;
  parentAccount?: string; // Slurm 父账号（fair-share 归属，可缺省）
  status: string; // active | suspended
  userCount: number;
}
export interface TenantsListResponse {
  tenants: TenantInfo[];
}
export interface UpdateTenantRequest {
  name?: string;
  status?: string; // active | suspended
  grpTRES?: string; // 如 "cpu=4,mem=8G"（Slurm GrpTRES 语法，白名单字符集）
  fairshare?: string; // 数字
}
export interface AdminUser {
  username: string;
  role: string; // admin | ops_admin | tenant_admin | member
  clusterUser?: string; // Slurm 集群映射用户（开通后回填）
  uid?: number;
  account?: string;
  tenantSlug?: string;
  status: string; // active | disabled
  displayName?: string;
  email?: string;
}
export interface TenantUsersResponse {
  users: AdminUser[];
}
export interface CreateAdminUserRequest {
  username: string;
  role: string;
  tenantSlug: string;
  password: string;
}
export interface AuditEntry {
  id: number;
  actor: string;
  action: string;
  target: string;
  detail?: string;
  requestId?: string;
  createdAt?: string;
}
export interface CreateTenantUserRequest {
  username: string;
  password: string;
  role: string; // member | tenant_admin
}

// ideFullURL 把后端返回的相对 web_url(/api/v1/ide/<sid>/) 拼成完整 URL 并附 ?token=<JWT>。
// 浏览器导航/iframe 无法带 Authorization 头，/ide/ 反代接受 ?token= 兜底（见 auth 中间件）。
export function ideFullURL(webUrl: string): string {
  const origin = API_BASE.replace(/\/api\/v1\/?$/, "");
  const sep = webUrl.includes("?") ? "&" : "?";
  return `${origin}${webUrl}${sep}token=${encodeURIComponent(getToken())}`;
}

export type NodeStateOp = "DRAIN" | "RESUME" | "IDLE";

// --- Slurm API ---
export const slurm = {
  // orgSlug 已退役（多租户 Phase 6：租户归属由用户库决定，登录不再传递）
  login: (username: string, password: string) =>
    apiFetch<LoginResponse>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),

  // 自助改密（成功后本人 token 全部失效，前端应跳回登录页）
  changePassword: (oldPassword: string, newPassword: string) =>
    apiFetch<{ message: string }>("/auth/password", {
      method: "POST",
      body: JSON.stringify({ oldPassword, newPassword }),
    }),

  // 集群状态：200 + pings[0].ping==="UP" 即 UP；503 抛错 → 调用方按 DEGRADED 处理
  getClusterStatus: () => apiFetch<PingResponse>("/slurm/ping"),

  getNodes: () => apiFetch<NodesListResponse>("/slurm/nodes"),

  // 监控快照：CPU/内存/GPU 分配 + /shared 磁盘用量（供监控页累积趋势图）
  getMonitorSnapshot: () => apiFetch<MonitorSnapshot>("/slurm/monitor/snapshot"),
  // 监控趋势历史（服务端持久化；后端未部署时调用方静默降级为空启动）
  getMonitorHistory: () => apiFetch<MonitorHistory>("/slurm/monitor/history"),
  updateNodeState: (name: string, state: NodeStateOp, reason?: string) =>
    apiFetch<NodeStateUpdateResponse>(
      `/slurm/nodes/${encodeURIComponent(name)}/state`,
      { method: "POST", body: JSON.stringify({ state, reason }) },
    ),

  // 作业（阶段 2）
  getJobs: () => apiFetch<JobListResponse>("/slurm/jobs"),
  getJobDetail: (jobId: number) =>
    apiFetch<JobDetail>(`/slurm/jobs/${jobId}/detail`),
  getJobHistory: (state?: string, limit?: number) => {
    const q = new URLSearchParams();
    if (state) q.set("state", state);
    if (limit) q.set("limit", String(limit));
    const s = q.toString();
    return apiFetch<HistoryResponse>(`/slurm/jobs/history${s ? "?" + s : ""}`);
  },
  submitJob: (payload: SubmitJobRequest) =>
    apiFetch<SubmitJobResponse>("/slurm/jobs/submit", {
      method: "POST",
      body: JSON.stringify(payload),
    }),
  cancelJob: (jobId: number) =>
    apiFetch<JobControlResponse>(`/slurm/jobs/${jobId}/cancel`, { method: "POST" }),
  holdJob: (jobId: number) =>
    apiFetch<JobControlResponse>(`/slurm/jobs/${jobId}/hold`, { method: "POST" }),
  requeueJob: (jobId: number) =>
    apiFetch<JobControlResponse>(`/slurm/jobs/${jobId}/requeue`, { method: "POST" }),

  // Web-IDE 会话（阶段 3）
  listContainers: () => apiFetch<ContainerListResponse>("/slurm/containers/list"),
  launchContainer: (payload: ContainerLaunchRequest) =>
    apiFetch<ContainerLaunchResponse>("/slurm/containers/launch", {
      method: "POST",
      body: JSON.stringify(payload),
    }),
  recycleContainer: (id: string) =>
    apiFetch<ContainerRecycleResponse>(`/slurm/containers/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),

  // 分区（阶段 4）
  getPartitions: () => apiFetch<PartitionsResponse>("/slurm/partitions"),

  // 计费（阶段 4）
  getBillingUsage: (user?: string, project?: string) => {
    const q = new URLSearchParams();
    if (user) q.set("user", user);
    if (project) q.set("project", project);
    const s = q.toString();
    return apiFetch<BillingUsage>(`/slurm/billing/usage${s ? "?" + s : ""}`);
  },
  exportBillingJSON: (user?: string, project?: string) => {
    const q = new URLSearchParams({ format: "json" });
    if (user) q.set("user", user);
    if (project) q.set("project", project);
    return apiFetch<BillingExportJSON>(`/slurm/billing/export?${q.toString()}`);
  },

  // 多租户管理（阶段 3）。写接口在后端以只读 yaml 存储运行时可能 503，
  // ApiError.message 已带后端 error 文本，调用方直接透出到页面 Notice 即可。
  // —— 平台管理员（admin）——
  listTenants: () => apiFetch<TenantsListResponse>("/admin/tenants"),
  createTenant: (slug: string, name: string) =>
    apiFetch<TenantInfo>("/admin/tenants", {
      method: "POST",
      body: JSON.stringify({ slug, name }),
    }),
  listAudit: (actor?: string, action?: string, limit?: number) => {
    const q = new URLSearchParams();
    if (actor) q.set("actor", actor);
    if (action) q.set("action", action);
    if (limit) q.set("limit", String(limit));
    const s = q.toString();
    return apiFetch<{ entries: AuditEntry[] }>(`/admin/audit${s ? "?" + s : ""}`);
  },
  updateTenant: (slug: string, payload: UpdateTenantRequest) =>
    apiFetch<{ tenant: TenantInfo }>(`/admin/tenants/${encodeURIComponent(slug)}`, {
      method: "PATCH",
      body: JSON.stringify(payload),
    }),
  createAdminUser: (payload: CreateAdminUserRequest) =>
    apiFetch<{ user: AdminUser }>("/admin/users", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  // —— 租户管理员（tenant_admin，仅本租户）——
  listMyTenantUsers: () => apiFetch<TenantUsersResponse>("/tenants/me/users"),
  createTenantUser: (payload: CreateTenantUserRequest) =>
    apiFetch<{ user: AdminUser }>("/tenants/me/users", {
      method: "POST",
      body: JSON.stringify(payload),
    }),
  updateTenantUser: (username: string, payload: { displayName?: string; status?: string }) =>
    apiFetch<{ user: AdminUser }>(`/tenants/me/users/${encodeURIComponent(username)}`, {
      method: "PATCH",
      body: JSON.stringify(payload),
    }),
  resetTenantUserPassword: (username: string, newPassword: string) =>
    apiFetch<{ message: string }>(`/tenants/me/users/${encodeURIComponent(username)}/password`, {
      method: "POST",
      body: JSON.stringify({ newPassword }),
    }),
};
