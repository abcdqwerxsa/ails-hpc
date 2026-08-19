package store_test

// 安全审计 2026-08-19 回归（store 层）：
//   - P1-8 挂起租户阻断登录/活体校验（TenantSuspended 投影 + Verify 拒绝）
//   - P1-6 LinkOIDC/UnlinkOIDC bump token_version（绑定/解绑吊销在途令牌）
//   - P2   JIT 开户独立复查 unixSafeRE（不再单层依赖 auth.sanitizeUsername）

import (
	"context"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/store"
)

func newSecurityStore(t *testing.T) store.AdminStore {
	t.Helper()
	stRaw, err := store.Open(t.TempDir() + "/sec.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = stRaw.Close() })
	st, ok := stRaw.(store.AdminStore)
	if !ok {
		t.Fatal("sqlite store must implement AdminStore")
	}
	if _, err := st.CreateTenant(context.Background(), "hpc-lab", ""); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return st
}

func TestSuspendedTenant_BlocksLoginAndLookup(t *testing.T) {
	st := newSecurityStore(t)
	ctx := context.Background()
	if _, err := st.CreateUser(ctx, store.NewUser{
		Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := st.SetTenantStatus(ctx, "hpc-lab", "suspended"); err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}

	if u, ok := st.(store.Store).Lookup("alice"); !ok || !u.TenantSuspended {
		t.Fatalf("Lookup: ok=%v TenantSuspended=%v, want true/true", ok, u.TenantSuspended)
	}
	if _, err := st.(store.Store).Verify("alice", "alice12345"); err != auth.ErrInvalidCredentials {
		t.Fatalf("Verify on suspended tenant: err=%v, want ErrInvalidCredentials", err)
	}
}

func TestLinkOIDC_BumpsTokenVersion(t *testing.T) {
	st := newSecurityStore(t)
	ctx := context.Background()
	if _, err := st.CreateUser(ctx, store.NewUser{
		Username: "bob", Password: "bob123456", Role: auth.RoleMember, TenantSlug: "hpc-lab",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	before, _ := st.(store.Store).UserVersion("bob")
	if err := st.LinkOIDC("bob", "sub-sec-1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	after, _ := st.(store.Store).UserVersion("bob")
	if after != before+1 {
		t.Fatalf("LinkOIDC token_version: %d → %d, want +1", before, after)
	}
	if err := st.UnlinkOIDC("bob"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	final, _ := st.(store.Store).UserVersion("bob")
	if final != after+1 {
		t.Fatalf("UnlinkOIDC token_version: %d → %d, want +1", after, final)
	}
}

func TestProvisionOIDCUser_RejectsUnsafeUsername(t *testing.T) {
	st := newSecurityStore(t)
	for _, bad := range []string{"root;id", "a/b", "-lead", "has space", "UPPER"} {
		if _, err := st.ProvisionOIDCUser(bad, "e@x", "d", auth.RoleMember, "hpc-lab", "sub-x"); err == nil {
			t.Errorf("username %q: want reject, got nil", bad)
		}
	}
}
