package main

// A2 审计补全回归：登录成功/失败、改密、作业提交与控制、IDE 操作全部落 audit_log
// （此前 audit_log 只覆盖管理面变更）。全栈夹具（真实路由 + sqlite 审计表）。

import (
	"net/http"
	"testing"

	"ails-hpc/pkg/auth"
)

// TestAudit_CoversAuthAndOps 审计面覆盖：登录成功/失败/锁定、作业提交与取消、IDE 启动。
func TestAudit_CoversAuthAndOps(t *testing.T) {
	r, st, _ := setupRBACStack(t)

	// 登录失败（错误密码）→ auth.login.fail
	doAuth(r, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"wrong!!!"}`, "")
	// 连续失败到锁定（默认 5 次窗口）→ auth.login.locked
	for i := 0; i < 6; i++ {
		doAuth(r, http.MethodPost, "/api/v1/auth/login", `{"username":"bob","password":"wrong!!!"}`, "")
	}
	// 换用户成功登录（bob 被限速不影响 alice）→ auth.login
	alice := loginViaAPI(t, r, "alice", "alice12345")
	// 作业提交 + 取消 → jobs.submit / jobs.cancel
	doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit", `{"name":"a1","script":"echo hi"}`, alice)
	doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/1/cancel", "", alice)
	// IDE 启动 → ide.launch
	doAuth(r, http.MethodPost, "/api/v1/slurm/containers/launch", `{"env_type":"vscode"}`, alice)
	// 改密成功 → auth.password.change（改完旧 token 吊销，审计已先行写入）
	doAuth(r, http.MethodPost, "/api/v1/auth/password", `{"oldPassword":"alice12345","newPassword":"Alice#12345x"}`, alice)

	entries, err := st.ListAudit(t.Context(), "", "", 100)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	got := map[string]int{}
	for _, e := range entries {
		got[e.Action]++
	}
	for _, want := range []string{"auth.login", "auth.login.fail", "auth.login.locked", "jobs.submit", "jobs.cancel", "ide.launch", "auth.password.change"} {
		if got[want] == 0 {
			t.Errorf("audit_log missing action %q (has %v)", want, got)
		}
	}
	// 提交审计的 actor/target 细节
	for _, e := range entries {
		if e.Action == "jobs.submit" {
			if e.Actor != "alice" {
				t.Errorf("jobs.submit actor = %q, want alice", e.Actor)
			}
			break
		}
	}
}

// TestAudit_LoginDetailShape 登录审计 detail 形状（ip/锁定态）——供运维检索。
func TestAudit_LoginDetailShape(t *testing.T) {
	r, st, _ := setupRBACStack(t)
	doAuth(r, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"nope!!!"}`, "")
	entries, err := st.ListAudit(t.Context(), "alice", "auth.login.fail", 10)
	if err != nil || len(entries) == 0 {
		t.Fatalf("fail entry missing: %v %v", entries, err)
	}
	if entries[0].Detail == "" || entries[0].Detail == "{}" {
		t.Errorf("login.fail detail should carry ip context, got %q", entries[0].Detail)
	}
}

// TestAudit_SinkNilNoop 测试装配（Audit=nil）不落审计也不炸。
func TestAudit_SinkNilNoop(t *testing.T) {
	r, _ := setupTestRouter(t) // 内存库装配：Auth 无 sink、Handlers.Audit 为 nil
	aliceTok := tokenFor(t, auth.RoleMember)
	if c, _ := doAuth(r, http.MethodPost, "/api/v1/slurm/jobs/submit", `{"name":"x","script":"echo hi"}`, aliceTok); c != http.StatusOK {
		t.Fatalf("submit with nil audit sink: %d", c)
	}
}
