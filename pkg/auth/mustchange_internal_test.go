package auth

// 安全审计 2026-08-19 P1-6 回归：must_change_password=1 的放行面不再含 /auth/oidc/*——
// 改密锁定窗口期可 bind SSO 植持久后门（LinkOIDC 历史
// 上不 bump token_version）。

import "testing"

func TestMustChangeAllowed_NoOIDCBind(t *testing.T) {
	allow := []string{
		"/api/v1/auth/password",
		"/api/v1/auth/me",
		"/api/v1/auth/me/sessions",
		"/api/v1/auth/logout-all",
	}
	for _, p := range allow {
		if !mustChangeAllowed(p) {
			t.Errorf("want allowed: %s", p)
		}
	}
	deny := []string{
		"/api/v1/auth/oidc/bind",
		"/api/v1/auth/oidc/unlink",
		"/api/v1/auth/oidc/anything",
		"/api/v1/slurm/jobs",
		"/api/v1/admin/users",
	}
	for _, p := range deny {
		if mustChangeAllowed(p) {
			t.Errorf("want denied: %s", p)
		}
	}
}
