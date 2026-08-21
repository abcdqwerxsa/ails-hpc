package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/services/admin"
	"ails-hpc/pkg/store"
)

// TestChallenger_M2_MultiTenantIsolationAndRBAC 深度对抗性测试：验证多租户隔离与 RBAC 边界 (Milestone 2)
func TestChallenger_M2_MultiTenantIsolationAndRBAC(t *testing.T) {
	r, st, adminSvc := setupRBACStack(t)

	var lastCommand string
	adminSvc.SetClusterRunner(func(args ...string) ([]byte, error) {
		lastCommand = strings.Join(args, " ")
		if strings.Contains(lastCommand, "show assoc") {
			return []byte("User|Account|QOS|DefQOS\n" +
				"alice|hpc-lab|normal,gpu-vip,high|normal\n" +
				"biomember|bio-lab|normal,bio-vip|normal\n" +
				"padmin|system|normal|normal\n"), nil
		}
		if strings.Contains(lastCommand, "show qos") {
			return []byte("Name|Priority|GrpTRES|MaxTRESPU|MaxWall|MaxJobsPU|MaxSubmitPU|Description\n" +
				"normal|0||||||Standard default\n" +
				"gpu-vip|1000|gres/gpu=4,cpu=32|gres/gpu=1,cpu=8|02:00:00|1|5|VIP GPU QOS\n" +
				"bio-vip|800|gres/gpu=2|gres/gpu=1|04:00:00|2|5|Bio Lab VIP\n" +
				"high|500||||||\n"), nil
		}
		if strings.Contains(lastCommand, "modify user") {
			if strings.Contains(lastCommand, "ghost") {
				return []byte("sacctmgr: error: Unknown user ghost\n"), nil
			}
			return []byte(""), nil
		}
		return []byte(""), nil
	})

	// 准备用户与凭证
	adminTok := loginViaAPI(t, r, "padmin", "platform123")  // Platform Admin (system tenant)
	tadminTok := loginViaAPI(t, r, "tadmin", "tenant12345") // Tenant Admin (hpc-lab tenant)
	memberTok := loginViaAPI(t, r, "alice", "alice12345")   // Member (hpc-lab tenant)
	opsTok := loginViaAPI(t, r, "puser", "puser123456")     // Ops Admin (puser)

	// 额外建一个 bio-lab 的 tenant admin 用于反向跨租户校验
	_, _ = st.CreateUser(context.Background(), store.NewUser{
		Username:   "bioadmin",
		Password:   "bioadmin123",
		Role:       auth.RoleTenantAdmin,
		TenantSlug: "bio-lab",
	})
	bioAdminTok := loginViaAPI(t, r, "bioadmin", "bioadmin123")

	validPayload := `{"defaultQos":"gpu-vip","allowedQos":["normal","gpu-vip"]}`

	// -------------------------------------------------------------
	// 1. Platform admin modifying arbitrary user QOS -> 200 OK
	// -------------------------------------------------------------
	t.Run("PlatformAdmin_ModifyArbitraryUserQOS_200", func(t *testing.T) {
		// Platform admin modifies user in hpc-lab (alice)
		code, body := doAuth(r, http.MethodPatch, "/api/v1/admin/users/alice/qos", validPayload, adminTok)
		if code != http.StatusOK {
			t.Fatalf("platform admin modify alice: want 200 got %d, body: %s", code, body)
		}

		// Platform admin modifies user in bio-lab (biomember)
		bioPayload := `{"defaultQos":"bio-vip","allowedQos":["normal","bio-vip"]}`
		code, body = doAuth(r, http.MethodPatch, "/api/v1/admin/users/biomember/qos", bioPayload, adminTok)
		if code != http.StatusOK {
			t.Fatalf("platform admin modify biomember: want 200 got %d, body: %s", code, body)
		}

		// Platform admin gets user QOS
		code, body = doAuth(r, http.MethodGet, "/api/v1/admin/users/biomember/qos", "", adminTok)
		if code != http.StatusOK {
			t.Fatalf("platform admin get biomember qos: want 200 got %d, body: %s", code, body)
		}
	})

	// -------------------------------------------------------------
	// 2. Tenant admin modifying member in own tenant -> 200 OK
	// -------------------------------------------------------------
	t.Run("TenantAdmin_ModifyOwnTenantMember_200", func(t *testing.T) {
		// tadmin (hpc-lab) modifies alice (hpc-lab) -> 200 OK
		code, body := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/qos", validPayload, tadminTok)
		if code != http.StatusOK {
			t.Fatalf("tenant admin modify own member: want 200 got %d, body: %s", code, body)
		}

		// bioadmin (bio-lab) modifies biomember (bio-lab) -> 200 OK
		bioPayload := `{"defaultQos":"bio-vip","allowedQos":["normal","bio-vip"]}`
		code, body = doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/biomember/qos", bioPayload, bioAdminTok)
		if code != http.StatusOK {
			t.Fatalf("bioadmin modify biomember: want 200 got %d, body: %s", code, body)
		}
	})

	// -------------------------------------------------------------
	// 3. Tenant admin attempting to modify user in another tenant -> 404 Not Found (MUST NOT BE 200 or 403)
	// -------------------------------------------------------------
	t.Run("TenantAdmin_ModifyCrossTenantUser_404_AntiIDOR", func(t *testing.T) {
		// tadmin (hpc-lab) tries to modify biomember (bio-lab)
		code, body := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/biomember/qos", validPayload, tadminTok)
		if code != http.StatusNotFound {
			t.Errorf("cross-tenant modification must return 404 Not Found (got %d, body: %s)", code, body)
		}
		if code == http.StatusForbidden || code == http.StatusOK {
			t.Fatalf("SECURITY VIOLATION: Cross-tenant modification returned %d (leaks user existence or allows cross-tenant write)", code)
		}

		// bioadmin (bio-lab) tries to modify alice (hpc-lab)
		code, body = doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/alice/qos", validPayload, bioAdminTok)
		if code != http.StatusNotFound {
			t.Errorf("bioadmin cross-tenant modification of alice must return 404 Not Found (got %d, body: %s)", code, body)
		}
	})

	// -------------------------------------------------------------
	// 4. Tenant admin attempting to modify platform admin -> 404 Not Found
	// -------------------------------------------------------------
	t.Run("TenantAdmin_ModifyPlatformAdmin_404_AntiPrivilegeEscalation", func(t *testing.T) {
		// tadmin (hpc-lab) tries to modify padmin (system admin)
		code, body := doAuth(r, http.MethodPatch, "/api/v1/tenants/me/users/padmin/qos", validPayload, tadminTok)
		if code != http.StatusNotFound {
			t.Errorf("tenant admin modifying platform admin must return 404 Not Found (got %d, body: %s)", code, body)
		}
		if code == http.StatusOK || code == http.StatusForbidden {
			t.Fatalf("SECURITY VIOLATION: tenant admin modifying platform admin returned %d", code)
		}
	})

	// -------------------------------------------------------------
	// 5. Regular member attempting to access QOS management routes -> 403 Forbidden
	// -------------------------------------------------------------
	t.Run("RegularMember_AccessManagementRoutes_403", func(t *testing.T) {
		restrictedRoutes := []struct {
			method string
			path   string
			body   string
		}{
			{http.MethodGet, "/api/v1/admin/users/alice/qos", ""},
			{http.MethodPatch, "/api/v1/admin/users/alice/qos", validPayload},
			{http.MethodPatch, "/api/v1/tenants/me/users/alice/qos", validPayload},
			{http.MethodGet, "/api/v1/admin/qos", ""},
			{http.MethodPost, "/api/v1/admin/qos", `{"name":"member-qos"}`},
			{http.MethodPatch, "/api/v1/admin/qos/gpu-vip", `{"priority":"10"}`},
			{http.MethodDelete, "/api/v1/admin/qos/gpu-vip", ""},
			{http.MethodPatch, "/api/v1/admin/tenants/hpc-lab/qos", `{"name":"gpu-vip"}`},
		}

		for _, rr := range restrictedRoutes {
			code, body := doAuth(r, rr.method, rr.path, rr.body, memberTok)
			if code != http.StatusForbidden {
				t.Errorf("member calling %s %s: want 403 Forbidden, got %d (body: %s)", rr.method, rr.path, code, body)
			}
		}
	})

	// -------------------------------------------------------------
	// 6. Unauthenticated access -> 401 Unauthorized
	// -------------------------------------------------------------
	t.Run("UnauthenticatedAccess_401", func(t *testing.T) {
		unauthRoutes := []struct {
			method string
			path   string
			body   string
		}{
			{http.MethodGet, "/api/v1/slurm/qos/available", ""},
			{http.MethodGet, "/api/v1/admin/users/alice/qos", ""},
			{http.MethodPatch, "/api/v1/admin/users/alice/qos", validPayload},
			{http.MethodPatch, "/api/v1/tenants/me/users/alice/qos", validPayload},
			{http.MethodGet, "/api/v1/admin/qos", ""},
			{http.MethodPost, "/api/v1/admin/qos", `{"name":"unauth-qos"}`},
		}

		for _, ur := range unauthRoutes {
			code, _ := doAuth(r, ur.method, ur.path, ur.body, "")
			if code != http.StatusUnauthorized {
				t.Errorf("unauthenticated request to %s %s: want 401 Unauthorized, got %d", ur.method, ur.path, code)
			}
		}
	})

	// -------------------------------------------------------------
	// 7. Authenticated user querying /slurm/qos/available -> 200 OK
	// -------------------------------------------------------------
	t.Run("AuthenticatedUser_GetAvailableQOS_200", func(t *testing.T) {
		tokens := []struct {
			role string
			tok  string
		}{
			{"platform_admin", adminTok},
			{"tenant_admin", tadminTok},
			{"member", memberTok},
			{"ops_admin", opsTok},
		}

		for _, user := range tokens {
			code, body := doAuth(r, http.MethodGet, "/api/v1/slurm/qos/available", "", user.tok)
			if code != http.StatusOK {
				t.Errorf("available QOS for %s: want 200 OK, got %d (body: %s)", user.role, code, body)
			}
			var resp admin.AvailableQOSResponse
			if err := json.Unmarshal([]byte(body), &resp); err != nil {
				t.Errorf("failed to parse AvailableQOSResponse for %s: %v", user.role, err)
			}
			if resp.DefaultQOS == "" {
				t.Errorf("missing defaultQos for %s", user.role)
			}
			if len(resp.AllowedQOS) == 0 {
				t.Errorf("allowedQos is empty for %s", user.role)
			}
		}
	})

	// -------------------------------------------------------------
	// 8. Adversarial Injections & Malformed User QOS Updates -> 400 Bad Request
	// -------------------------------------------------------------
	t.Run("UserQOS_InjectionAndMalformedPayloads_400", func(t *testing.T) {
		badPayloads := []struct {
			name    string
			payload string
		}{
			{"Semicolon in defaultQos", `{"defaultQos":"vip;rm -rf","allowedQos":["normal","vip;rm -rf"]}`},
			{"Quote in defaultQos", `{"defaultQos":"vip' || id","allowedQos":["normal","vip' || id"]}`},
			{"Subshell in allowedQos", `{"defaultQos":"normal","allowedQos":["normal","$(whoami)"]}`},
			{"Default not in allowed", `{"defaultQos":"gpu-vip","allowedQos":["normal","high"]}`},
			{"Empty object", `{}`},
			{"Empty allowed and default", `{"defaultQos":"","allowedQos":[]}`},
			{"Malformed JSON", `{"defaultQos": `},
		}

		for _, tc := range badPayloads {
			code, _ := doAuth(r, http.MethodPatch, "/api/v1/admin/users/alice/qos", tc.payload, adminTok)
			if code != http.StatusBadRequest {
				t.Errorf("%s: expected 400 Bad Request, got %d", tc.name, code)
			}
		}
	})

	// -------------------------------------------------------------
	// 9. Reset Keyword (-1) Handling
	// -------------------------------------------------------------
	t.Run("UserQOS_ResetKeyword_200", func(t *testing.T) {
		resetPayload := `{"defaultQos":"-1","allowedQos":["-1"]}`
		code, body := doAuth(r, http.MethodPatch, "/api/v1/admin/users/alice/qos", resetPayload, adminTok)
		if code != http.StatusOK {
			t.Fatalf("reset payload failed: want 200 got %d (body: %s)", code, body)
		}
	})
}
