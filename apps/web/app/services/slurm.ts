// Slurm apiserver（Go，:8090）的 typed API 客户端。
// JWT 由 login 写入 localStorage('ails_token')，每个请求自动附 Authorization 头。
// 错误体走统一信封 {error, request_id?, ...extras}；非 2xx 抛 ApiError(message=error)。
//
// 注：API_BASE 当前指向生产 apiserver（与 login 一致）。阶段 5（gin 服务 React 构建产物、
// 同源）后改为相对 "/api/v1" 即可。
const API_BASE = "http://192.168.20.226:8090/api/v1";

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

// 后续阶段用的类型（先占位，字段在各自阶段对齐）
export interface JobSummary {
  job_id: number;
  name: string;
  job_state: string;
  partition?: string;
  nodes?: string;
  [k: string]: unknown;
}
export interface JobListResponse {
  code?: number;
  jobs: JobSummary[];
}

export type NodeStateOp = "DRAIN" | "RESUME" | "IDLE";

// --- Slurm API ---
export const slurm = {
  login: (username: string, password: string, orgSlug: string) =>
    apiFetch<LoginResponse>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password, orgSlug }),
    }),

  // 集群状态：200 + pings[0].ping==="UP" 即 UP；503 抛错 → 调用方按 DEGRADED 处理
  getClusterStatus: () => apiFetch<PingResponse>("/slurm/ping"),

  getNodes: () => apiFetch<NodesListResponse>("/slurm/nodes"),
  updateNodeState: (name: string, state: NodeStateOp, reason?: string) =>
    apiFetch<NodeStateUpdateResponse>(
      `/slurm/nodes/${encodeURIComponent(name)}/state`,
      { method: "POST", body: JSON.stringify({ state, reason }) },
    ),

  // 下列为阶段 2-4 预留（已 typed，未在阶段 1 页面使用）
  getJobs: () => apiFetch<JobListResponse>("/slurm/jobs"),
  getPartitions: () => apiFetch<unknown>("/slurm/partitions"),
};
