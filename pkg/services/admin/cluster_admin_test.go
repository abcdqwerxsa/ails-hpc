package admin

// 分区管理单测（v2 增量）：解析、字段白名单校验、scontrol 命令语法、审计落库。
// runner 直接注入（内部测试可触 Service.runner），审计面用真实 sqlite store。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"ails-hpc/pkg/store"
)

// cannedPartitionShow 是 scontrol show partition debug 的样例输出（21.08 格式）。
const cannedPartitionShow = `PartitionName=debug
   AllowGroups=ALL AllowAccounts=ALL DenyAccounts=ALL AllowQos=ALL
   Default=YES OverSubscribe=YES MaxTime=UNLIMITED DefMemPerCPU=UNLIMITED
   State=UP Nodes=c1[1-2] TotalNodes=2 TotalCPUs=8 SelectTypeParameters=NONE
`

func newPartitionService(t *testing.T, runner clusterRunner) (*Service, store.AdminStore) {
	t.Helper()
	stRaw, err := store.Open(filepath.Join(t.TempDir(), "partitions.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = stRaw.Close() })
	st := stRaw.(store.AdminStore)
	s := NewService(st, nil)
	if runner != nil {
		s.runner = runner
	}
	return s, st
}

func TestParsePartitionDetail(t *testing.T) {
	d := parsePartitionDetail(cannedPartitionShow)
	if d == nil {
		t.Fatal("parse returned nil for valid scontrol output")
	}
	want := map[string]string{
		"Name": "debug", "State": "UP", "Default": "YES", "MaxTime": "UNLIMITED",
		"DefMemPerCPU": "UNLIMITED", "Nodes": "c1[1-2]", "OverSubscribe": "YES",
		"AllowAccounts": "ALL", "AllowGroups": "ALL",
	}
	got := map[string]string{
		"Name": d.Name, "State": d.State, "Default": d.Default, "MaxTime": d.MaxTime,
		"DefMemPerCPU": d.DefMemPerCPU, "Nodes": d.Nodes, "OverSubscribe": d.OverSubscribe,
		"AllowAccounts": d.AllowAccounts, "AllowGroups": d.AllowGroups,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}

	// 无 PartitionName=（不存在/报错输出）→ nil
	for _, out := range []string{"", "error: Invalid partition name nope\n", "some unrelated text"} {
		if d := parsePartitionDetail(out); d != nil {
			t.Errorf("parsePartitionDetail(%q) = %+v, want nil", out, d)
		}
	}
}

func TestGetPartition(t *testing.T) {
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte(cannedPartitionShow), nil
	})
	d, err := s.GetPartition(context.Background(), "debug")
	if err != nil {
		t.Fatalf("GetPartition: %v", err)
	}
	if d.Name != "debug" || d.State != "UP" || d.Default != "YES" {
		t.Errorf("GetPartition = %+v", d)
	}

	// scontrol 报错文本（无 PartitionName=）→ ErrPartitionNotFound
	s2, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte("error: Invalid partition name nope"), nil
	})
	if _, err := s2.GetPartition(context.Background(), "nope"); err != ErrPartitionNotFound {
		t.Errorf("missing partition: want ErrPartitionNotFound got %v", err)
	}

	// 名字字符集外 → 直接拒绝（不触 runner）
	s3, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		t.Fatal("runner must not be called for invalid name")
		return nil, nil
	})
	if _, err := s3.GetPartition(context.Background(), "bad name!"); err == nil {
		t.Error("invalid partition name must be rejected")
	}
}

func TestUpdatePartition_CommandSyntax(t *testing.T) {
	var got string
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		got = strings.Join(args, " ")
		return []byte(""), nil
	})
	err := s.UpdatePartition(context.Background(), "padmin", "debug", PartitionUpdates{
		State: "DOWN", MaxTime: "1-00:00:00",
	}, "rid-1")
	if err != nil {
		t.Fatalf("UpdatePartition: %v", err)
	}
	// 引号拼接与字段顺序（partition= 前置，其余按 partitionFields 声明序）与 CreateReservation 同构
	want := `sh -c scontrol update 'partition=debug' 'State=DOWN' 'MaxTime=1-00:00:00' 2>&1`
	if got != want {
		t.Errorf("runner args:\n got  %s\n want %s", got, want)
	}
}

func TestUpdatePartition_Validation(t *testing.T) {
	s, _ := newPartitionService(t, nil) // 校验失败不应触 runner（默认 runner 也不该被执行）
	cases := []struct {
		name string
		u    PartitionUpdates
	}{
		{"empty updates", PartitionUpdates{}},
		{"state enum", PartitionUpdates{State: "SIDEWAYS"}},
		{"default enum", PartitionUpdates{Default: "MAYBE"}},
		{"maxtime charset", PartitionUpdates{MaxTime: "one hour"}},
		{"mem charset", PartitionUpdates{DefMemPerCPU: "4GB of ram"}},
		{"oversubscribe enum", PartitionUpdates{OverSubscribe: "ALOT"}},
		{"nodes charset", PartitionUpdates{Nodes: "c1;rm -rf"}},
		{"accounts charset", PartitionUpdates{AllowAccounts: "acc`ct"}},
		{"groups charset", PartitionUpdates{AllowGroups: "grp$(x)"}},
	}
	for _, tc := range cases {
		if err := s.UpdatePartition(context.Background(), "padmin", "debug", tc.u, ""); err == nil {
			t.Errorf("%s: want validation error, got nil", tc.name)
		}
	}
	// 分区名字符集外 → 拒绝
	if err := s.UpdatePartition(context.Background(), "padmin", "bad name!", PartitionUpdates{State: "UP"}, ""); err == nil {
		t.Error("invalid partition name must be rejected")
	}
	// 合法值集合逐字段放行
	for _, u := range []PartitionUpdates{
		{State: "UP"}, {State: "inactive"}, {Default: "YES"}, {MaxTime: "unlimited"},
		{MaxTime: "90"}, {MaxTime: "2:30:00"}, {MaxTime: "7-00:00:00"}, {DefMemPerCPU: "4096"},
		{DefMemPerCPU: "4G"}, {OverSubscribe: "FORCE:2"}, {Nodes: "c1,c2[3-5]"},
		{AllowAccounts: "ails_hpc_lab"}, {AllowGroups: "ALL"},
	} {
		if err := ValidatePartitionUpdates(u); err != nil {
			t.Errorf("ValidatePartitionUpdates(%+v): unexpected error %v", u, err)
		}
	}
}

func TestUpdatePartition_ErrorSniff(t *testing.T) {
	// scontrol 经 sh -c 2>&1 抓回的 stderr 含 Error → 报错（与 CreateReservation 同判定）
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte("Error: Invalid partition name nope"), nil
	})
	if err := s.UpdatePartition(context.Background(), "padmin", "nope", PartitionUpdates{State: "UP"}, ""); err == nil {
		t.Error("scontrol error output must surface as error")
	}
}

func TestUpdatePartition_Audit(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte(""), nil
	})
	ctx := context.Background()
	if err := s.UpdatePartition(ctx, "padmin", "debug", PartitionUpdates{State: "DOWN"}, "rid-audit"); err != nil {
		t.Fatalf("UpdatePartition: %v", err)
	}
	entries, err := st.ListAudit(ctx, "padmin", "partition.update", 10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1 (got %+v)", len(entries), entries)
	}
	e := entries[0]
	if e.Target != "partition:debug" || e.RequestID != "rid-audit" {
		t.Errorf("audit entry = %+v", e)
	}
	if !strings.Contains(e.Detail, `"state":"DOWN"`) {
		t.Errorf("audit detail should carry updates JSON, got %q", e.Detail)
	}
}

// TestClusterAdminAudit_X1 v3-X1：预约/QOS 四个写操作全部落审计
// （reservations.create/delete、qos.create、tenant.qos——此前管理面审计缺口）。
func TestClusterAdminAudit_X1(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte(""), nil // 全部命令空输出=成功（scontrol/sacctmgr 报错都走 stderr→sh 2>&1）
	})
	ctx := context.Background()
	if _, err := st.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	if _, err := s.CreateReservation(ctx, "padmin", "maint", "", 30, "", "u1", "", "rid-1"); err != nil {
		t.Fatalf("CreateReservation: %v", err)
	}
	if err := s.DeleteReservation(ctx, "padmin", "maint", "rid-2"); err != nil {
		t.Fatalf("DeleteReservation: %v", err)
	}
	if _, err := s.CreateQOS(ctx, "padmin", "qos-a", "", "rid-3"); err != nil {
		t.Fatalf("CreateQOS: %v", err)
	}
	if err := s.SetTenantQOS(ctx, "padmin", "hpc-lab", "qos-a", "rid-4"); err != nil {
		t.Fatalf("SetTenantQOS: %v", err)
	}

	for _, w := range []struct{ action, target, rid string }{
		{"reservations.create", "reservation:maint", "rid-1"},
		{"reservations.delete", "reservation:maint", "rid-2"},
		{"qos.create", "qos:qos-a", "rid-3"},
		{"tenant.qos", "tenant:hpc-lab", "rid-4"},
	} {
		entries, err := st.ListAudit(ctx, "padmin", w.action, 10)
		if err != nil {
			t.Fatalf("ListAudit %s: %v", w.action, err)
		}
		found := false
		for _, e := range entries {
			if e.Target == w.target && e.RequestID == w.rid {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("audit %s %s (rid=%s) missing; entries=%+v", w.action, w.target, w.rid, entries)
		}
	}
}

// TestListTenantQuotas v4-W3：sacctmgr account 表 → 租户配额关联；空输出容错。
func TestListTenantQuotas(t *testing.T) {
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte("hpc-lab|cpu=32,mem=64G\nbio-lab|\nroot|cpu=9999\n"), nil
	})
	ctx := context.Background()
	// 建两个租户（parent_account=slug）
	if err := rawCreateTenant(t, s, "hpc-lab"); err != nil {
		t.Fatal(err)
	}
	if err := rawCreateTenant(t, s, "bio-lab"); err != nil {
		t.Fatal(err)
	}

	qs, err := s.ListTenantQuotas(ctx)
	if err != nil {
		t.Fatalf("ListTenantQuotas: %v", err)
	}
	got := map[string]string{}
	for _, q := range qs {
		got[q.TenantSlug] = q.GrpTRES
	}
	if got["hpc-lab"] != "cpu=32,mem=64G" || got["bio-lab"] != "" {
		t.Errorf("quotas = %v", got)
	}
	if _, hasRoot := got["root"]; hasRoot {
		t.Errorf("non-tenant account leaked into quotas: %v", got)
	}
}

// rawCreateTenant 经 store 接口建租户（cluster_admin_test 夹具辅助）。
func rawCreateTenant(t *testing.T, s *Service, slug string) error {
	t.Helper()
	_, err := s.st.CreateTenant(context.Background(), slug, "")
	return err
}
