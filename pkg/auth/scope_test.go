package auth_test

import (
	"testing"

	"ails-hpc/pkg/auth"
)

// TestScope_FromClaims 按 authoritative role 推导可见范围；未知角色最保守（Self）。
func TestScope_FromClaims(t *testing.T) {
	cases := []struct {
		name string
		role string
		want auth.ScopeMode
	}{
		{"member→Self", auth.RoleMember, auth.ScopeSelf},
		{"unknown→Self(保守)", "intern", auth.ScopeSelf},
		{"tenant_admin→Tenant", auth.RoleTenantAdmin, auth.ScopeTenant},
		{"ops_admin→All", auth.RoleOpsAdmin, auth.ScopeAll},
		{"admin→All", auth.RoleSystemAdmin, auth.ScopeAll},
	}
	for _, tc := range cases {
		sc := auth.ScopeFromClaims(&auth.Claims{Role: tc.role, Username: "u", ClusterUser: "cu", OrgSlug: "hpc-lab"})
		if sc.Mode != tc.want {
			t.Errorf("%s: Mode=%v want %v", tc.name, sc.Mode, tc.want)
		}
	}
	// nil claims → 零值（Self+空身份，AllowsUser 恒 false）
	if sc := auth.ScopeFromClaims(nil); sc.AllowsUser("anyone") {
		t.Error("nil claims must allow nobody")
	}
}

// TestScope_TenantFallback 迁移期租户标识回退：TID 优先，空则 OrgSlug。
func TestScope_TenantFallback(t *testing.T) {
	sc := auth.ScopeFromClaims(&auth.Claims{Role: auth.RoleTenantAdmin, OrgSlug: "hpc-lab"})
	if sc.TenantSlug != "hpc-lab" {
		t.Errorf("fallback tenant = %q, want hpc-lab", sc.TenantSlug)
	}
	sc2 := auth.ScopeFromClaims(&auth.Claims{Role: auth.RoleTenantAdmin, OrgSlug: "hpc-lab", TID: "lab-x"})
	if sc2.TenantSlug != "lab-x" {
		t.Errorf("TID priority: tenant = %q, want lab-x", sc2.TenantSlug)
	}
}

// TestScope_AllowsUser Self 仅本人可见；Tenant/All 由列表过滤收口（恒真）。
func TestScope_AllowsUser(t *testing.T) {
	self := auth.ScopeFromClaims(&auth.Claims{Role: auth.RoleMember, ClusterUser: "ailsmember"})
	if !self.AllowsUser("ailsmember") || self.AllowsUser("ailsops") {
		t.Error("ScopeSelf must allow only the caller's clusterUser")
	}
	tenant := auth.ScopeFromClaims(&auth.Claims{Role: auth.RoleTenantAdmin, OrgSlug: "hpc-lab"})
	if !tenant.AllowsUser("anyone") {
		t.Error("ScopeTenant is list-filtered downstream; AllowsUser itself is permissive")
	}
}
