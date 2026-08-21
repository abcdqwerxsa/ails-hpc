package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestRouter_QOS_Adversarial_RBACAndInjection 测试全方位的 REST 路由安全、注入与 RBAC 拦截
func TestRouter_QOS_Adversarial_RBACAndInjection(t *testing.T) {
	r, _, adminSvc := setupRBACStack(t)

	var lastCommand string
	adminSvc.SetClusterRunner(func(args ...string) ([]byte, error) {
		lastCommand = strings.Join(args, " ")
		if strings.Contains(lastCommand, "show qos") {
			return []byte("normal|0||||||\ngpu-vip|1000|gres/gpu=4,cpu=32|gres/gpu=1,cpu=8|02:00:00|1|5|VIP GPU QOS\n"), nil
		}
		if strings.Contains(lastCommand, "modify qos ghost") || strings.Contains(lastCommand, "delete qos ghost") {
			return []byte("sacctmgr: error: Unknown QOS: ghost\n"), nil
		}
		if strings.Contains(lastCommand, "modify qos none") || strings.Contains(lastCommand, "delete qos none") {
			return []byte(" Nothing modified\n"), nil
		}
		return []byte(""), nil
	})

	adminTok := loginViaAPI(t, r, "padmin", "platform123")
	memberTok := loginViaAPI(t, r, "alice", "alice12345")
	tadminTok := loginViaAPI(t, r, "tadmin", "tenant12345")

	// 1. RBAC 完整矩阵测试
	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/admin/qos", ""},
		{http.MethodGet, "/api/v1/admin/qos/gpu-vip", ""},
		{http.MethodPost, "/api/v1/admin/qos", `{"name":"adv-qos","priority":"10"}`},
		{http.MethodPatch, "/api/v1/admin/qos/gpu-vip", `{"priority":"20"}`},
		{http.MethodDelete, "/api/v1/admin/qos/gpu-vip", ""},
		{http.MethodPatch, "/api/v1/admin/tenants/default/qos", `{"name":"gpu-vip"}`},
		{http.MethodGet, "/api/v1/admin/users/alice/qos", ""},
		{http.MethodPatch, "/api/v1/admin/users/alice/qos", `{"defaultQOS":"gpu-vip","allowedQOS":["normal","gpu-vip"]}`},
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("RBAC_401_%s_%s", ep.method, ep.path), func(t *testing.T) {
			code, _ := doAuth(r, ep.method, ep.path, ep.body, "")
			if code != http.StatusUnauthorized {
				t.Errorf("expected 401 for unauthenticated request to %s %s, got %d", ep.method, ep.path, code)
			}
		})

		t.Run(fmt.Sprintf("RBAC_403_Member_%s_%s", ep.method, ep.path), func(t *testing.T) {
			code, _ := doAuth(r, ep.method, ep.path, ep.body, memberTok)
			if code != http.StatusForbidden {
				t.Errorf("expected 403 for member request to %s %s, got %d", ep.method, ep.path, code)
			}
		})

		t.Run(fmt.Sprintf("RBAC_403_TenantAdmin_%s_%s", ep.method, ep.path), func(t *testing.T) {
			code, _ := doAuth(r, ep.method, ep.path, ep.body, tadminTok)
			if code != http.StatusForbidden {
				t.Errorf("expected 403 for tenant admin request to %s %s, got %d", ep.method, ep.path, code)
			}
		})
	}

	// 2. 各种注入 Payload 测试 POST /api/v1/admin/qos
	injectionPayloads := []struct {
		name    string
		payload string
	}{
		{"SQL/Shell injection in Name", `{"name": "gpu;reboot;", "priority": "100"}`},
		{"Single quote in Name", `{"name": "gpu' || id", "priority": "100"}`},
		{"Backtick in Name", "{\"name\": \"gpu`id`\", \"priority\": \"100\"}"},
		{"Subshell in Name", `{"name": "gpu$(whoami)", "priority": "100"}`},
		{"Semicolon in Priority", `{"name": "sec-qos", "priority": "100;rm -rf /"}`},
		{"Quote in Priority", `{"name": "sec-qos", "priority": "100' OR '1'='1"}`},
		{"Semicolon in GrpTRES", `{"name": "sec-qos", "grp_tres": "gres/gpu=4;reboot"}`},
		{"Single quote in GrpTRES", `{"name": "sec-qos", "grp_tres": "gres/gpu=4' --"}`},
		{"Semicolon in MaxTRESPerUser", `{"name": "sec-qos", "max_tres_per_user": "gres/gpu=1;kill -9 1"}`},
		{"Semicolon in MaxJobsPerUser", `{"name": "sec-qos", "max_jobs_per_user": "1;reboot"}`},
		{"Semicolon in MaxSubmitJobsPerUser", `{"name": "sec-qos", "max_submit_jobs_per_user": "5;reboot"}`},
		{"Semicolon in MaxWall", `{"name": "sec-qos", "max_wall": "02:00:00;rm -rf /"}`},
		{"Single quote in MaxWall", `{"name": "sec-qos", "max_wall": "02:00:00' || id"}`},
		{"Single quote in Description", `{"name": "sec-qos", "description": "malicious ' quote"}`},
		{"Double quote in Description", `{"name": "sec-qos", "description": "malicious \" quote"}`},
		{"Backtick in Description", "{\"name\": \"sec-qos\", \"description\": \"malicious `id`\"}"},
		{"Subshell in Description", `{"name": "sec-qos", "description": "malicious $(id)"}`},
		{"Semicolon in Description", `{"name": "sec-qos", "description": "malicious; rm -rf /"}`},
		{"Pipe in Description", `{"name": "sec-qos", "description": "malicious | cat /etc/passwd"}`},
	}

	for _, tc := range injectionPayloads {
		t.Run("Create_Injection_"+tc.name, func(t *testing.T) {
			lastCommand = ""
			code, _ := doAuth(r, http.MethodPost, "/api/v1/admin/qos", tc.payload, adminTok)
			if code != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request for %s, got %d", tc.name, code)
			}
			if lastCommand != "" && !strings.Contains(lastCommand, "show qos") {
				t.Errorf("cluster command was executed despite validation error: %q", lastCommand)
			}
		})
	}

	// 3. 各种注入 Payload 测试 PATCH /api/v1/admin/qos/:name (针对更新字段)
	patchFieldInjectionPayloads := []struct {
		name    string
		payload string
	}{
		{"Semicolon in Priority", `{"priority": "100;rm -rf /"}`},
		{"Quote in Priority", `{"priority": "100' OR '1'='1"}`},
		{"Semicolon in GrpTRES", `{"grp_tres": "gres/gpu=4;reboot"}`},
		{"Single quote in GrpTRES", `{"grp_tres": "gres/gpu=4' --"}`},
		{"Semicolon in MaxTRESPerUser", `{"max_tres_per_user": "gres/gpu=1;kill -9 1"}`},
		{"Semicolon in MaxJobsPerUser", `{"max_jobs_per_user": "1;reboot"}`},
		{"Semicolon in MaxSubmitJobsPerUser", `{"max_submit_jobs_per_user": "5;reboot"}`},
		{"Semicolon in MaxWall", `{"max_wall": "02:00:00;rm -rf /"}`},
		{"Single quote in MaxWall", `{"max_wall": "02:00:00' || id"}`},
		{"Single quote in Description", `{"description": "malicious ' quote"}`},
		{"Double quote in Description", `{"description": "malicious \" quote"}`},
		{"Backtick in Description", "{\"description\": \"malicious `id`\"}"},
		{"Subshell in Description", `{"description": "malicious $(id)"}`},
		{"Semicolon in Description", `{"description": "malicious; rm -rf /"}`},
		{"Pipe in Description", `{"description": "malicious | cat /etc/passwd"}`},
	}

	for _, tc := range patchFieldInjectionPayloads {
		t.Run("Patch_Injection_"+tc.name, func(t *testing.T) {
			lastCommand = ""
			code, _ := doAuth(r, http.MethodPatch, "/api/v1/admin/qos/gpu-vip", tc.payload, adminTok)
			if code != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request for PATCH %s, got %d", tc.name, code)
			}
		})
	}

	// 4. URL 路径参数注入测试
	t.Run("URL_Param_Injection", func(t *testing.T) {
		badPaths := []string{
			"/api/v1/admin/qos/bad;name",
			"/api/v1/admin/qos/bad'name",
			"/api/v1/admin/qos/bad`name`",
		}
		for _, path := range badPaths {
			code, _ := doAuth(r, http.MethodGet, path, "", adminTok)
			if code != http.StatusBadRequest && code != http.StatusNotFound {
				t.Errorf("GET %s expected 400/404, got %d", path, code)
			}
			code, _ = doAuth(r, http.MethodDelete, path, "", adminTok)
			if code != http.StatusBadRequest && code != http.StatusNotFound {
				t.Errorf("DELETE %s expected 400/404, got %d", path, code)
			}
		}
	})

	// 5. 删除 normal QOS 保护
	t.Run("ProtectNormalQOS_HTTP", func(t *testing.T) {
		for _, name := range []string{"normal", "NORMAL", "Normal"} {
			code, body := doAuth(r, http.MethodDelete, "/api/v1/admin/qos/"+name, "", adminTok)
			if code != http.StatusBadRequest {
				t.Errorf("DELETE normal (%s) expected 400, got %d (body: %s)", name, code, body)
			}
		}
	})

	// 6. 畸变 JSON Payload
	t.Run("MalformedJSONPayload", func(t *testing.T) {
		malformed := []string{
			`{`,
			`{"name": `,
			`{"name": "ok", "priority": }`,
			`null`,
			`"just a string"`,
			`[1, 2, 3]`,
		}
		for _, raw := range malformed {
			code, _ := doAuth(r, http.MethodPost, "/api/v1/admin/qos", raw, adminTok)
			if code != http.StatusBadRequest {
				t.Errorf("expected 400 for malformed JSON %q, got %d", raw, code)
			}
		}
	})

	// 7. 更新/删除不存在的 QOS (sacctmgr 输出 Unknown QOS / Nothing modified / Nothing deleted) 应返回 404 Not Found
	t.Run("NonExistentQOS_Returns_404", func(t *testing.T) {
		// PATCH nonexistent
		code, body := doAuth(r, http.MethodPatch, "/api/v1/admin/qos/ghost", `{"priority":"100"}`, adminTok)
		if code != http.StatusNotFound {
			t.Errorf("PATCH nonexistent QOS 'ghost' expected 404 Not Found, got %d (body: %s)", code, body)
		}

		// DELETE nonexistent
		code, body = doAuth(r, http.MethodDelete, "/api/v1/admin/qos/ghost", "", adminTok)
		if code != http.StatusNotFound {
			t.Errorf("DELETE nonexistent QOS 'ghost' expected 404 Not Found, got %d (body: %s)", code, body)
		}
	})
}
