package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ails-hpc/pkg/auth"
)

// newTestStore 在临时目录开一个真 sqlite 库（跑真迁移，无 mock）。
func newTestStore(t *testing.T) Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// writeYaml 写一个最小 users.yaml（bcrypt 用 MinCost 省时）。
func writeYaml(t *testing.T, dir string) string {
	t.Helper()
	hash := func(pw string) string {
		h, _ := auth.BcryptGenerateFromPassword(pw)
		return h
	}
	yaml := fmt.Sprintf(`users:
  - username: admin
    password_hash: "%s"
    role: admin
    orgSlug: hpc-lab
    tenantNs: default
    clusterUser: ailsadmin
    uid: 2001
    gid: 2000
    account: ailsadmin
  - username: member
    password_hash: "%s"
    role: member
    orgSlug: hpc-lab
    tenantNs: default
    clusterUser: ailsmember
    uid: 2003
    gid: 2000
    account: ailsmember
  - username: bioadmin
    password_hash: "%s"
    role: tenant_admin
    orgSlug: bio-lab
    tenantNs: default
    clusterUser: ailbio
    uid: 2010
    gid: 2000
    account: ailbio
`, hash("admin123"), hash("member123"), hash("bio123"))
	p := filepath.Join(dir, "users.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportAndReadPath(t *testing.T) {
	st := newTestStore(t)
	yamlPath := writeYaml(t, t.TempDir())

	n, err := ImportYaml(st, yamlPath)
	if err != nil {
		t.Fatalf("ImportYaml: %v", err)
	}
	if n != 3 {
		t.Fatalf("imported %d users, want 3", n)
	}

	// 租户：system（保留）+ hpc-lab + bio-lab（按 orgSlug 去重）
	tenants, err := st.Tenants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tn := range tenants {
		got[tn.Slug] = true
		if tn.ParentAccount != tn.Slug {
			t.Errorf("tenant %s parent_account=%q, want = slug", tn.Slug, tn.ParentAccount)
		}
	}
	for _, want := range []string{"system", "hpc-lab", "bio-lab"} {
		if !got[want] {
			t.Errorf("tenant %q missing; have %v", want, got)
		}
	}

	// Lookup/Verify：哈希原样保留 → 原密码可登录；TenantSlug/OrgSlug/Status 派生正确
	u, ok := st.Lookup("member")
	if !ok {
		t.Fatal("Lookup(member) miss")
	}
	if u.ClusterUser != "ailsmember" || u.Role != auth.RoleMember || u.UID != 2003 {
		t.Errorf("member mapping wrong: %+v", u)
	}
	if u.TenantSlug != "hpc-lab" || u.OrgSlug != "hpc-lab" || u.Status != "active" {
		t.Errorf("derived fields wrong: tenant=%q org=%q status=%q", u.TenantSlug, u.OrgSlug, u.Status)
	}
	if _, ok := st.Lookup("ghost"); ok {
		t.Error("ghost must not exist")
	}
	if v, err := st.Verify("member", "member123"); err != nil || v.Username != "member" {
		t.Errorf("Verify correct password: %v", err)
	}
	if _, err := st.Verify("member", "wrong"); err != auth.ErrInvalidCredentials {
		t.Errorf("Verify wrong password: want ErrInvalidCredentials, got %v", err)
	}

	// ListUsers 全量
	if len(st.ListUsers()) != 3 {
		t.Errorf("ListUsers = %d, want 3", len(st.ListUsers()))
	}

	// ClusterUsersOfTenant：按租户成员（active）收口
	hp, err := st.ClusterUsersOfTenant(context.Background(), "hpc-lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(hp) != 2 || hp[0] != "ailsadmin" || hp[1] != "ailsmember" {
		t.Errorf("hpc-lab members = %v, want [ailsadmin ailsmember]", hp)
	}
	bio, _ := st.ClusterUsersOfTenant(context.Background(), "bio-lab")
	if len(bio) != 1 || bio[0] != "ailbio" {
		t.Errorf("bio-lab members = %v", bio)
	}
	if sys, _ := st.ClusterUsersOfTenant(context.Background(), "system"); len(sys) != 0 {
		t.Errorf("system members = %v, want empty", sys)
	}
}

func TestImportIdempotent(t *testing.T) {
	st := newTestStore(t)
	yamlPath := writeYaml(t, t.TempDir())
	for i := 0; i < 2; i++ {
		if _, err := ImportYaml(st, yamlPath); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if n := len(st.ListUsers()); n != 3 {
		t.Errorf("after 2 imports ListUsers = %d, want 3 (upsert not duplicate)", n)
	}
	if v, err := st.Verify("bioadmin", "bio123"); err != nil || v.Username != "bioadmin" {
		t.Errorf("re-import must preserve credentials: %v", err)
	}
}

func TestOpenMigratesIdempotently(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.db")
	for i := 0; i < 2; i++ {
		st, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i+1, err)
		}
		_ = st.Close()
	}
}

func TestExportSeeds(t *testing.T) {
	st := newTestStore(t)
	yamlPath := writeYaml(t, t.TempDir())
	if _, err := ImportYaml(st, yamlPath); err != nil {
		t.Fatal(err)
	}
	sd, err := ExportSeeds(st)
	if err != nil {
		t.Fatal(err)
	}
	// 租户：system+hpc-lab+bio-lab，parentAccount=slug
	ts := map[string]string{}
	for _, tn := range sd.Tenants {
		ts[tn.Slug] = tn.ParentAccount
	}
	if len(ts) != 3 || ts["hpc-lab"] != "hpc-lab" || ts["system"] != "system" {
		t.Errorf("tenants = %v", ts)
	}
	// 用户：clusterUser/account/uid/tenant 完整（entrypoint 供给用）
	byCU := map[string]SeedUser{}
	for _, u := range sd.Users {
		byCU[u.ClusterUser] = u
	}
	m := byCU["ailsmember"]
	if m.Username != "member" || m.Account != "ailsmember" || m.UID != 2003 || m.TenantSlug != "hpc-lab" || m.GID != 2000 {
		t.Errorf("ailsmember seed = %+v", m)
	}
	// JSON 往返（entrypoint 用 python json 消费）
	var buf bytes.Buffer
	if err := WriteSeedsJSON(&buf, st); err != nil {
		t.Fatal(err)
	}
	var back Seeds
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if len(back.Users) != len(sd.Users) || len(back.Tenants) != len(sd.Tenants) {
		t.Error("roundtrip count mismatch")
	}
}
