package main

// A3 矩阵测试扩展：自定义角色 × 路由的表驱动覆盖。对每个路由权限点，构造
// 「恰好持有该权限」与「恰好缺失该权限」的自定义角色，断言 200/403 与内置矩阵
// 语义一致（docs/rbac-matrix.md §5 的执行面证据）。
//
// 作用域规则（与生产一致）：平台自定义角色（base=ops_admin）只可指派给 system
// 租户用户 → 平台用例的 subject 是 puser（ops_admin@system）；租户自定义角色
// （base=member）指派给本租户用户 → 租户用例的 subject 是 alice（member@hpc-lab）。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"ails-hpc/pkg/auth"
)

// TestRouteMatrix_CustomRoles 自定义角色逐权限执行：持有→放行，缺失→403。
func TestRouteMatrix_CustomRoles(t *testing.T) {
	r, _ := setupRBACStack(t)
	tadmin := loginViaAPI(t, r, "tadmin", "tenant12345")
	padmin := loginViaAPI(t, r, "padmin", "platform123")

	cases := []struct {
		name    string
		subject string // 被改派的用户（其登录态发起请求）
		passwd  string
		perms   []string
		method  string // "@submit-then-cancel" = 特例：先提交再取消自己的作业
		path    string
		body    string
		creator string // 创建者 token（决定角色作用域与子集校验基线）
		assign  string // /api/v1/admin | /api/v1/tenants/me
		want    int
	}{
		// 持有 → 放行
		{"c-drain", "puser", "puser123456", []string{auth.PermClusterRead, auth.PermNodesManage},
			http.MethodPost, "/api/v1/slurm/nodes/node1/state", `{"state":"DRAIN"}`, padmin, "/api/v1/admin", http.StatusOK},
		{"c-submit", "alice", "alice12345", []string{auth.PermClusterRead, auth.PermJobsSubmit},
			http.MethodPost, "/api/v1/slurm/jobs/submit", `{"name":"m1","script":"echo hi"}`, tadmin, "/api/v1/tenants/me", http.StatusOK},
		{"c-control", "alice", "alice12345", []string{auth.PermClusterRead, auth.PermJobsSubmit, auth.PermJobsControl},
			"@submit-then-cancel", "", "", tadmin, "/api/v1/tenants/me", http.StatusOK},
		{"c-idelist", "alice", "alice12345", []string{auth.PermClusterRead, auth.PermIdeList},
			http.MethodGet, "/api/v1/slurm/containers/list", "", tadmin, "/api/v1/tenants/me", http.StatusOK},
		{"c-billing", "alice", "alice12345", []string{auth.PermClusterRead, auth.PermBillingRead},
			http.MethodGet, "/api/v1/slurm/billing/usage", "", tadmin, "/api/v1/tenants/me", http.StatusOK},
		{"c-audit", "puser", "puser123456", []string{auth.PermClusterRead, auth.PermAuditRead},
			http.MethodGet, "/api/v1/admin/audit", "", padmin, "/api/v1/admin", http.StatusOK},
		// 缺失 → 403
		{"x-drain", "puser", "puser123456", []string{auth.PermClusterRead},
			http.MethodPost, "/api/v1/slurm/nodes/node1/state", `{"state":"DRAIN"}`, padmin, "/api/v1/admin", http.StatusForbidden},
		{"x-submit", "alice", "alice12345", []string{auth.PermClusterRead},
			http.MethodPost, "/api/v1/slurm/jobs/submit", `{"name":"m2","script":"echo hi"}`, tadmin, "/api/v1/tenants/me", http.StatusForbidden},
		{"x-billing", "alice", "alice12345", []string{auth.PermClusterRead, auth.PermJobsSubmit},
			http.MethodGet, "/api/v1/slurm/billing/usage", "", tadmin, "/api/v1/tenants/me", http.StatusForbidden},
		{"x-idelist", "alice", "alice12345", []string{auth.PermClusterRead},
			http.MethodGet, "/api/v1/slurm/containers/list", "", tadmin, "/api/v1/tenants/me", http.StatusForbidden},
		{"x-adminroles", "puser", "puser123456", []string{auth.PermClusterRead},
			http.MethodGet, "/api/v1/admin/roles", "", padmin, "/api/v1/admin", http.StatusForbidden},
		{"x-tenantroles", "alice", "alice12345", []string{auth.PermClusterRead},
			http.MethodGet, "/api/v1/tenants/me/roles", "", tadmin, "/api/v1/tenants/me", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 复位（subject 改派回内置基角色 → 测试角色不在用，可删可重建）
			resetRole, resetPath := `"member"`, tc.assign+"/users/alice/role"
			if tc.subject == "puser" {
				resetRole, resetPath = `"ops_admin"`, tc.assign+"/users/puser/role"
			}
			doAuth(r, http.MethodPatch, resetPath, `{"role":`+resetRole+`}`, tc.creator)
			doAuth(r, http.MethodDelete, tc.assign+"/roles/"+tc.name, "", tc.creator)

			permJSON := "["
			for i, p := range tc.perms {
				if i > 0 {
					permJSON += ","
				}
				permJSON += `"` + p + `"`
			}
			permJSON += "]"
			base, scopePath := "member", "/api/v1/tenants/me/roles"
			if tc.assign == "/api/v1/admin" {
				base, scopePath = "ops_admin", "/api/v1/admin/roles"
			}
			code, body := doAuth(r, http.MethodPost, scopePath,
				fmt.Sprintf(`{"name":%q,"permissions":%s,"baseRole":%q}`, tc.name, permJSON, base), tc.creator)
			if code != http.StatusOK {
				t.Fatalf("create role: %d %s", code, body)
			}
			if code, b := doAuth(r, http.MethodPatch, tc.assign+"/users/"+tc.subject+"/role",
				fmt.Sprintf(`{"role":%q}`, tc.name), tc.creator); code != http.StatusOK {
				t.Fatalf("assign: %d %s", code, b)
			}

			subject := loginViaAPI(t, r, tc.subject, tc.passwd)
			if tc.method == "@submit-then-cancel" {
				// jobs:control 正向路径：先以本人身份提交，再取消自己的作业
				code, body := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit",
					`{"name":"ctl","script":"echo hi"}`, subject)
				if code != http.StatusOK {
					t.Fatalf("pre-submit: %d %s", code, body)
				}
				var resp struct {
					JobID int `json:"job_id"`
				}
				_ = json.Unmarshal([]byte(body), &resp)
				got := doRequest(r, http.MethodPost,
					fmt.Sprintf("/api/v1/slurm/jobs/%d/cancel", resp.JobID), "", subject)
				if got != tc.want {
					t.Errorf("cancel own job with jobs:control: want %d got %d", tc.want, got)
				}
				return
			}
			if got := doRequest(r, tc.method, tc.path, tc.body, subject); got != tc.want {
				t.Errorf("%s %s as custom role %v: want %d got %d",
					tc.method, tc.path, tc.perms, tc.want, got)
			}
		})
	}
}
