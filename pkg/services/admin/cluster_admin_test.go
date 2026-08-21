package admin

// 分区管理单测（v2 增量）：解析、字段白名单校验、scontrol 命令语法、审计落库。
// runner 直接注入（内部测试可触 Service.runner），审计面用真实 sqlite store。

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"
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
	if _, err := s.CreateQOS(ctx, "padmin", "qos-a", QOSUpdates{}, "rid-3"); err != nil {
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

func TestParseQOSList_HeaderAware(t *testing.T) {
	out := "Name|Priority|GrpTRES|MaxTRESPU|MaxTRES|MaxJobsPU|MaxSubmitPU|MaxWall|Description\n" +
		"normal|0|||||||Standard default QOS\n" +
		"gpu-vip|1000|gres/gpu=4,cpu=32|gres/gpu=1,cpu=8|gres/gpu=2|1|5|02:00:00|VIP GPU QOS\n"

	qos := ParseQOSList(out)
	if len(qos) != 2 {
		t.Fatalf("ParseQOSList header aware got %d want 2", len(qos))
	}
	if qos[0].Name != "normal" || qos[0].Priority != "0" || qos[0].Description != "Standard default QOS" {
		t.Errorf("qos[0] = %+v", qos[0])
	}
	vip := qos[1]
	if vip.Name != "gpu-vip" || vip.Priority != "1000" || vip.GrpTRES != "gres/gpu=4,cpu=32" ||
		vip.MaxTRESPerUser != "gres/gpu=1,cpu=8" || vip.MaxJobsPerUser != "1" ||
		vip.MaxSubmitJobsPerUser != "5" || vip.MaxWall != "02:00:00" || vip.MaxWallDuration != "02:00:00" ||
		vip.Description != "VIP GPU QOS" {
		t.Errorf("qos[1] = %+v", vip)
	}
}

func TestParseQOSList_NoHeader(t *testing.T) {
	cases := []struct {
		name     string
		canned   string
		wantLen  int
		validate func(t *testing.T, qos []QOS)
	}{
		{
			name: "standard 7/8-field positional format",
			canned: "normal|0||||||\n" +
				"gpu-vip|1000|gres/gpu=4,cpu=32|gres/gpu=1,cpu=8|02:00:00|1|5|VIP GPU\n",
			wantLen: 2,
			validate: func(t *testing.T, qos []QOS) {
				if qos[0].Name != "normal" || qos[0].Priority != "0" {
					t.Errorf("qos[0] = %+v", qos[0])
				}
				vip := qos[1]
				if vip.Name != "gpu-vip" || vip.Priority != "1000" ||
					vip.GrpTRES != "gres/gpu=4,cpu=32" || vip.MaxTRES != "gres/gpu=1,cpu=8" ||
					vip.MaxTRESPerUser != "gres/gpu=1,cpu=8" || vip.MaxWall != "02:00:00" ||
					vip.MaxJobs != "1" || vip.MaxJobsPerUser != "1" ||
					vip.MaxSubmitJobsPerUser != "5" || vip.Description != "VIP GPU" {
					t.Errorf("vip = %+v", vip)
				}
			},
		},
		{
			name: "fallback 6-field format",
			canned: "normal|0||||\n" +
				"high|500|cpu=16|cpu=4|01:00:00|2\n",
			wantLen: 2,
			validate: func(t *testing.T, qos []QOS) {
				if qos[1].Name != "high" || qos[1].Priority != "500" || qos[1].MaxWall != "01:00:00" || qos[1].MaxJobs != "2" {
					t.Errorf("fallback qos[1] = %+v", qos[1])
				}
			},
		},
		{
			name:    "empty and whitespace output",
			canned:  "\n\n   \n\t\n",
			wantLen: 0,
		},
		{
			name: "malformed rows and delimiters",
			canned: "||||||\n" +
				"bad_row_without_pipes\n" +
				"partial|100\n" +
				"valid_after|200||||||\n",
			wantLen: 2,
			validate: func(t *testing.T, qos []QOS) {
				if qos[0].Name != "partial" || qos[0].Priority != "100" {
					t.Errorf("partial = %+v", qos[0])
				}
				if qos[1].Name != "valid_after" || qos[1].Priority != "200" {
					t.Errorf("valid_after = %+v", qos[1])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseQOSList(tc.canned)
			if len(got) != tc.wantLen {
				t.Fatalf("got len %d want %d", len(got), tc.wantLen)
			}
			if tc.validate != nil {
				tc.validate(t, got)
			}
		})
	}
}

func TestListQOS_Parsing(t *testing.T) {
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte("normal|0||||||\ngpu-vip|1000|gres/gpu=4,cpu=32|gres/gpu=1,cpu=8|02:00:00|1|5|\n"), nil
	})
	got, err := s.ListQOS(context.Background())
	if err != nil {
		t.Fatalf("ListQOS: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListQOS len = %d, want 2", len(got))
	}
	if got[1].Name != "gpu-vip" || got[1].Priority != "1000" {
		t.Errorf("got[1] = %+v", got[1])
	}

	// Runner error
	sErr, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		return nil, errors.New("sacctmgr connection error")
	})
	if _, err := sErr.ListQOS(context.Background()); err == nil {
		t.Error("expected error on runner failure")
	}
}

func TestGetQOS(t *testing.T) {
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte("normal|0||||||\ngpu-vip|1000|gres/gpu=4,cpu=32|gres/gpu=1,cpu=8|02:00:00|1|5|\n"), nil
	})
	q, err := s.GetQOS(context.Background(), "gpu-vip")
	if err != nil {
		t.Fatalf("GetQOS: %v", err)
	}
	if q.Name != "gpu-vip" || q.Priority != "1000" {
		t.Errorf("GetQOS got %+v", q)
	}

	// Non-existent
	if _, err := s.GetQOS(context.Background(), "nope"); !errors.Is(err, ErrQOSNotFound) {
		t.Errorf("want ErrQOSNotFound, got %v", err)
	}

	// Invalid name
	if _, err := s.GetQOS(context.Background(), "bad;name"); err == nil {
		t.Error("invalid qos name must be rejected")
	}
}

func TestCreateQOS_CommandSyntax(t *testing.T) {
	var allCalls []string
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		allCalls = append(allCalls, strings.Join(args, " "))
		return []byte(""), nil
	})

	updates := QOSUpdates{
		Priority:             "1000",
		GrpTRES:              "gres/gpu=4,cpu=32",
		MaxTRESPerUser:       "gres/gpu=1,cpu=8",
		MaxJobsPerUser:       "1",
		MaxSubmitJobsPerUser: "5",
		MaxWallDuration:      "02:00:00",
		Description:          "VIP GPU QOS",
	}

	_, err := s.CreateQOS(context.Background(), "padmin", "gpu-vip", updates, "req-create-1")
	if err != nil {
		t.Fatalf("CreateQOS: %v", err)
	}

	if len(allCalls) == 0 {
		t.Fatal("no commands executed")
	}
	cmdStr := allCalls[0]
	expectedPrefix := "sacctmgr -i add qos gpu-vip"
	if !strings.Contains(cmdStr, expectedPrefix) {
		t.Errorf("command missing prefix: got %q, want contains %q", cmdStr, expectedPrefix)
	}

	expectedTokens := []string{
		"Priority=1000",
		"GrpTRES=gres/gpu=4,cpu=32",
		"MaxTRESPerUser=gres/gpu=1,cpu=8",
		"MaxJobsPerUser=1",
		"MaxSubmitJobsPerUser=5",
		"MaxWallDuration=02:00:00",
		"Description=VIP GPU QOS",
	}
	for _, token := range expectedTokens {
		if !strings.Contains(cmdStr, token) {
			t.Errorf("command missing expected token %q: full cmd = %q", token, cmdStr)
		}
	}
}

func TestCreateQOS_ValidationAndAntiInjection(t *testing.T) {
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		t.Fatalf("runner MUST NOT be invoked when validation fails: args = %v", args)
		return nil, nil
	})

	cases := []struct {
		name       string
		qosName    string
		updates    QOSUpdates
		wantErrMsg string
	}{
		// Name 校验
		{"empty name", "", QOSUpdates{}, "invalid qos name"},
		{"digit prefix", "1qos", QOSUpdates{}, "invalid qos name"},
		{"underscore prefix", "_qos", QOSUpdates{}, "invalid qos name"},
		{"contains spaces", "gpu vip", QOSUpdates{}, "invalid qos name"},
		{"shell semicolon injection", "qos;rm -rf /", QOSUpdates{}, "invalid qos name"},
		{"shell quote injection", "qos' || id || '", QOSUpdates{}, "invalid qos name"},
		{"command substitution", "qos$(whoami)", QOSUpdates{}, "invalid qos name"},
		{"too long name (>32 chars)", strings.Repeat("a", 33), QOSUpdates{}, "invalid qos name"},

		// Priority 校验
		{"negative priority", "test-qos", QOSUpdates{Priority: "-10"}, "invalid Priority"},
		{"non-numeric priority", "test-qos", QOSUpdates{Priority: "high"}, "invalid Priority"},
		{"priority shell injection", "test-qos", QOSUpdates{Priority: "100;reboot"}, "invalid Priority"},

		// TRES 校验
		{"malformed GrpTRES", "test-qos", QOSUpdates{GrpTRES: "=4"}, "invalid GrpTRES"},
		{"negative TRES count", "test-qos", QOSUpdates{GrpTRES: "cpu=-2"}, "invalid GrpTRES"},
		{"TRES shell injection", "test-qos", QOSUpdates{GrpTRES: "gres/gpu=1;touch /tmp/pwn"}, "invalid GrpTRES"},
		{"TRES quote escape", "test-qos", QOSUpdates{MaxTRESPerUser: "gres/gpu=1' || id"}, "invalid MaxTRESPerUser"},

		// Jobs 限制校验
		{"negative MaxJobsPerUser", "test-qos", QOSUpdates{MaxJobsPerUser: "-10"}, "invalid MaxJobsPerUser"},
		{"negative MaxSubmitJobsPerUser", "test-qos", QOSUpdates{MaxSubmitJobsPerUser: "-5"}, "invalid MaxSubmitJobsPerUser"},

		// MaxWall 校验
		{"invalid walltime format", "test-qos", QOSUpdates{MaxWallDuration: "2hours"}, "invalid MaxWallDuration"},
		{"walltime injection", "test-qos", QOSUpdates{MaxWallDuration: "01:00:00; id"}, "invalid MaxWallDuration"},

		// Description 校验
		{"description with quotes", "test-qos", QOSUpdates{Description: "test' || reboot"}, "invalid Description"},
		{"description with newline", "test-qos", QOSUpdates{Description: "test\nreboot"}, "invalid Description"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.CreateQOS(context.Background(), "padmin", tc.qosName, tc.updates, "req-fail")
			if err == nil {
				t.Fatalf("case %s: expected error, got nil", tc.name)
			}
			if tc.wantErrMsg != "" && !strings.Contains(err.Error(), tc.wantErrMsg) {
				t.Errorf("error %q does not contain expected %q", err.Error(), tc.wantErrMsg)
			}
		})
	}
}

func TestUpdateQOS_CommandSyntax(t *testing.T) {
	var capturedArgs []string
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		capturedArgs = args
		return []byte(""), nil
	})

	// 1. 单字段更新
	err := s.UpdateQOS(context.Background(), "padmin", "gpu-vip", QOSUpdates{
		Priority: "2000",
	}, "req-up-1")
	if err != nil {
		t.Fatalf("UpdateQOS single field: %v", err)
	}
	cmd1 := strings.Join(capturedArgs, " ")
	if !strings.Contains(cmd1, "sacctmgr -i modify qos gpu-vip set 'Priority=2000'") {
		t.Errorf("got %q, want contains modify set Priority=2000", cmd1)
	}

	// 2. 多字段组合更新
	err = s.UpdateQOS(context.Background(), "padmin", "gpu-vip", QOSUpdates{
		MaxJobsPerUser:  "2",
		MaxWallDuration: "04:00:00",
	}, "req-up-2")
	if err != nil {
		t.Fatalf("UpdateQOS multi field: %v", err)
	}
	cmd2 := strings.Join(capturedArgs, " ")
	if !strings.Contains(cmd2, "MaxJobsPerUser=2") || !strings.Contains(cmd2, "MaxWallDuration=04:00:00") {
		t.Errorf("multi field command missing tokens: got %q", cmd2)
	}
}

func TestUpdateQOS_Validation(t *testing.T) {
	s, _ := newPartitionService(t, nil)

	// 空修改体拒绝
	if err := s.UpdateQOS(context.Background(), "padmin", "gpu-vip", QOSUpdates{}, "req-empty"); err == nil {
		t.Error("empty QOSUpdates must be rejected")
	}

	// 非法名字拒绝
	if err := s.UpdateQOS(context.Background(), "padmin", "bad;name", QOSUpdates{Priority: "100"}, "req-badname"); err == nil {
		t.Error("invalid qos name must be rejected")
	}

	// 非法字段值拒绝
	if err := s.UpdateQOS(context.Background(), "padmin", "gpu-vip", QOSUpdates{Priority: "-10"}, "req-badval"); err == nil {
		t.Error("invalid priority value must be rejected")
	}
}

func TestDeleteQOS(t *testing.T) {
	var capturedArgs []string
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		capturedArgs = args
		return []byte(""), nil
	})

	// 正常删除自定义 QOS
	err := s.DeleteQOS(context.Background(), "padmin", "temp-qos", "req-del-1")
	if err != nil {
		t.Fatalf("DeleteQOS: %v", err)
	}
	cmdStr := strings.Join(capturedArgs, " ")
	if !strings.Contains(cmdStr, "sacctmgr -i delete qos temp-qos") {
		t.Errorf("got %q, want contains delete qos temp-qos", cmdStr)
	}

	// 非法名称拦截
	if err := s.DeleteQOS(context.Background(), "padmin", "bad;name", "req-del-2"); err == nil {
		t.Error("invalid qos name in DeleteQOS must be rejected")
	}

	// 尝试删除保留系统默认 normal QOS 保护（平台业务守卫）
	if err := s.DeleteQOS(context.Background(), "padmin", "normal", "req-del-3"); err == nil {
		t.Error("deleting default 'normal' QOS should be blocked by policy")
	}
}

func TestUpdateQOS_NotFound(t *testing.T) {
	for _, canned := range []string{
		"sacctmgr: error: Unknown QOS: ghost-qos",
		"sacctmgr: error: Unknown QOS 'ghost-qos'",
		" Nothing modified",
		" Nothing modified\n",
	} {
		s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			return []byte(canned), nil
		})
		err := s.UpdateQOS(context.Background(), "padmin", "ghost-qos", QOSUpdates{Priority: "100"}, "req-up-404")
		if !errors.Is(err, ErrQOSNotFound) {
			t.Errorf("UpdateQOS output %q: want ErrQOSNotFound, got %v", canned, err)
		}
	}
}

func TestDeleteQOS_NotFound(t *testing.T) {
	for _, canned := range []string{
		"sacctmgr: error: Unknown QOS: ghost-qos",
		"sacctmgr: error: Unknown QOS 'ghost-qos'",
		" Nothing deleted",
		" Nothing deleted\n",
	} {
		s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			return []byte(canned), nil
		})
		err := s.DeleteQOS(context.Background(), "padmin", "ghost-qos", "req-del-404")
		if !errors.Is(err, ErrQOSNotFound) {
			t.Errorf("DeleteQOS output %q: want ErrQOSNotFound, got %v", canned, err)
		}
	}
}

func TestQOS_AuditLogs(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte(""), nil
	})
	ctx := context.Background()

	// 1. Create QOS Audit
	updates := QOSUpdates{
		Priority:       "500",
		GrpTRES:        "gres/gpu=2",
		MaxJobsPerUser: "2",
	}
	_, err := s.CreateQOS(ctx, "padmin", "audit-qos", updates, "rid-audit-create")
	if err != nil {
		t.Fatalf("CreateQOS: %v", err)
	}

	// 2. Update QOS Audit
	err = s.UpdateQOS(ctx, "padmin", "audit-qos", QOSUpdates{Priority: "800"}, "rid-audit-update")
	if err != nil {
		t.Fatalf("UpdateQOS: %v", err)
	}

	// 3. Delete QOS Audit
	err = s.DeleteQOS(ctx, "padmin", "audit-qos", "rid-audit-delete")
	if err != nil {
		t.Fatalf("DeleteQOS: %v", err)
	}

	// 检索并断言审计记录
	expectedAudits := []struct {
		action       string
		target       string
		rid          string
		assertDetail func(t *testing.T, detail string)
	}{
		{
			action: "qos.create",
			target: "qos:audit-qos",
			rid:    "rid-audit-create",
			assertDetail: func(t *testing.T, detail string) {
				if !strings.Contains(detail, `"priority":"500"`) || !strings.Contains(detail, `"grpTRES":"gres/gpu=2"`) {
					t.Errorf("create audit detail missing fields: got %q", detail)
				}
			},
		},
		{
			action: "qos.modify",
			target: "qos:audit-qos",
			rid:    "rid-audit-update",
			assertDetail: func(t *testing.T, detail string) {
				if !strings.Contains(detail, `"priority":"800"`) {
					t.Errorf("update audit detail missing fields: got %q", detail)
				}
			},
		},
		{
			action: "qos.delete",
			target: "qos:audit-qos",
			rid:    "rid-audit-delete",
			assertDetail: func(t *testing.T, detail string) {
				if detail == "" {
					t.Error("delete audit detail should not be empty")
				}
			},
		},
	}

	for _, want := range expectedAudits {
		entries, err := st.ListAudit(ctx, "padmin", want.action, 10)
		if err != nil {
			t.Fatalf("ListAudit %s: %v", want.action, err)
		}
		var match *store.AuditEntry
		for i := range entries {
			if entries[i].Target == want.target && entries[i].RequestID == want.rid {
				match = &entries[i]
				break
			}
		}
		if match == nil {
			t.Fatalf("missing audit entry for action=%s, target=%s, rid=%s (entries: %+v)", want.action, want.target, want.rid, entries)
		}
		if match.Actor != "padmin" {
			t.Errorf("audit actor = %q, want padmin", match.Actor)
		}
		if want.assertDetail != nil {
			want.assertDetail(t, match.Detail)
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

func TestValidateUserQOSUpdates(t *testing.T) {
	cases := []struct {
		name    string
		updates UserQOSUpdates
		wantErr bool
	}{
		{
			name:    "valid single qos",
			updates: UserQOSUpdates{DefaultQOS: "normal", AllowedQOS: []string{"normal"}},
			wantErr: false,
		},
		{
			name:    "valid multiple qos",
			updates: UserQOSUpdates{DefaultQOS: "gpu-vip", AllowedQOS: []string{"normal", "gpu-vip", "high"}},
			wantErr: false,
		},
		{
			name:    "empty default and allowed",
			updates: UserQOSUpdates{},
			wantErr: true,
		},
		{
			name:    "default not in allowed",
			updates: UserQOSUpdates{DefaultQOS: "vip", AllowedQOS: []string{"normal", "high"}},
			wantErr: true,
		},
		{
			name:    "invalid default qos name injection",
			updates: UserQOSUpdates{DefaultQOS: "vip;reboot", AllowedQOS: []string{"vip;reboot"}},
			wantErr: true,
		},
		{
			name:    "invalid allowed qos name injection",
			updates: UserQOSUpdates{DefaultQOS: "normal", AllowedQOS: []string{"normal", "vip' || id"}},
			wantErr: true,
		},
		{
			name:    "reset -1 allowed",
			updates: UserQOSUpdates{DefaultQOS: "-1", AllowedQOS: []string{"-1"}},
			wantErr: false,
		},
		{
			name:    "whitespace and deduplication",
			updates: UserQOSUpdates{DefaultQOS: " normal ", AllowedQOS: []string{" normal ", "gpu-vip", " normal "}},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateUserQOSUpdates(&tc.updates)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateUserQOSUpdates() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestSetUserQOS_HappyPath_And_CommandSyntax(t *testing.T) {
	var capturedCmds []string
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		capturedCmds = append(capturedCmds, strings.Join(args, " "))
		return []byte(""), nil
	})
	ctx := context.Background()

	// Seed tenant & user
	if _, err := st.CreateTenant(ctx, "hpc-lab", ""); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := st.CreateUser(ctx, store.NewUser{
		Username:   "alice",
		Password:   "alice12345",
		Role:       auth.RoleMember,
		TenantSlug: "hpc-lab",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	req := UserQOSUpdates{
		DefaultQOS: "gpu-vip",
		AllowedQOS: []string{"normal", "gpu-vip", "high"},
	}
	err := s.SetUserQOS(ctx, "padmin", "alice", "", req, "req-qos-1")
	if err != nil {
		t.Fatalf("SetUserQOS failed: %v", err)
	}

	if len(capturedCmds) < 2 {
		t.Fatalf("expected at least 2 commands (modify + reconfigure), got %d: %v", len(capturedCmds), capturedCmds)
	}

	modifyCmd := capturedCmds[0]
	if !strings.Contains(modifyCmd, "sacctmgr -i modify user alice account=hpc-lab") {
		t.Errorf("modifyCmd prefix mismatch: %s", modifyCmd)
	}
	if !strings.Contains(modifyCmd, "qos=normal,gpu-vip,high") {
		t.Errorf("modifyCmd missing qos: %s", modifyCmd)
	}
	if !strings.Contains(modifyCmd, "defaultqos=gpu-vip") {
		t.Errorf("modifyCmd missing defaultqos: %s", modifyCmd)
	}

	reconfigCmd := capturedCmds[1]
	if !strings.Contains(reconfigCmd, "scontrol reconfigure") {
		t.Errorf("reconfigCmd mismatch: %s", reconfigCmd)
	}

	// Verify Audit Log
	entries, err := st.ListAudit(ctx, "padmin", "qos.user.set", 10)
	if err != nil || len(entries) == 0 {
		t.Fatalf("audit log missing: %v", err)
	}
	if entries[0].Target != "user:alice" || entries[0].RequestID != "req-qos-1" {
		t.Errorf("audit entry mismatch: %+v", entries[0])
	}
	if !strings.Contains(entries[0].Detail, "gpu-vip") {
		t.Errorf("audit detail missing qos: %s", entries[0].Detail)
	}
}

func TestSetUserQOS_TenantAdmin_Scoping_And_Protection(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte(""), nil
	})
	ctx := context.Background()

	_, _ = st.CreateTenant(ctx, "hpc-lab", "")
	_, _ = st.CreateTenant(ctx, "bio-lab", "")

	_, _ = st.CreateUser(ctx, store.NewUser{Username: "padmin", Password: "padmin12345", Role: auth.RoleSystemAdmin, TenantSlug: "system"})
	_, _ = st.CreateUser(ctx, store.NewUser{Username: "tadmin", Password: "tadmin12345", Role: auth.RoleTenantAdmin, TenantSlug: "hpc-lab"})
	_, _ = st.CreateUser(ctx, store.NewUser{Username: "alice", Password: "alice123456", Role: auth.RoleMember, TenantSlug: "hpc-lab"})
	_, _ = st.CreateUser(ctx, store.NewUser{Username: "biomember", Password: "biomember12", Role: auth.RoleMember, TenantSlug: "bio-lab"})

	req := UserQOSUpdates{
		DefaultQOS: "normal",
		AllowedQOS: []string{"normal"},
	}

	// 1. Tenant admin updates own tenant user -> OK
	if err := s.SetUserQOS(ctx, "tadmin", "alice", "hpc-lab", req, "rid-1"); err != nil {
		t.Errorf("tenant admin update own user should succeed: %v", err)
	}

	// 2. Tenant admin updates cross-tenant user -> store.ErrNotFound (404 anti-enumeration)
	if err := s.SetUserQOS(ctx, "tadmin", "biomember", "hpc-lab", req, "rid-2"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("tenant admin update cross tenant user want ErrNotFound, got %v", err)
	}

	// 3. Tenant admin updates platform admin padmin -> store.ErrNotFound (padmin in system tenant)
	if err := s.SetUserQOS(ctx, "tadmin", "padmin", "hpc-lab", req, "rid-3"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("tenant admin update padmin want ErrNotFound, got %v", err)
	}
}

func TestSetUserQOS_Validation_And_AntiInjection(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		if strings.Contains(cmd, "Unknown QOS") {
			return []byte("sacctmgr: error: Unknown QOS: ghost\n"), nil
		}
		if strings.Contains(cmd, "slurm_error") {
			return []byte("sacctmgr: error: Problem modifying user\n"), nil
		}
		return []byte(""), nil
	})
	ctx := context.Background()

	_, _ = st.CreateTenant(ctx, "hpc-lab", "")
	_, _ = st.CreateUser(ctx, store.NewUser{Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab"})

	validReq := UserQOSUpdates{DefaultQOS: "normal", AllowedQOS: []string{"normal"}}

	// Username injection
	for _, badUser := range []string{"alice;rm -rf /", "alice' || id", "alice$(whoami)", "alice bad"} {
		if err := s.SetUserQOS(ctx, "padmin", badUser, "", validReq, ""); err == nil {
			t.Errorf("bad username %q should be rejected", badUser)
		}
	}

	// Tenant slug injection
	for _, badTenant := range []string{"hpc-lab;reboot", "hpc'lab", "hpc$(id)"} {
		if err := s.SetUserQOS(ctx, "padmin", "alice", badTenant, validReq, ""); err == nil {
			t.Errorf("bad tenant slug %q should be rejected", badTenant)
		}
	}

	// User not found
	if err := s.SetUserQOS(ctx, "padmin", "nonexistent", "", validReq, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("nonexistent user want ErrNotFound, got %v", err)
	}

	// ReadOnlyStore
	sReadOnly := NewService(nil, nil)
	if err := sReadOnly.SetUserQOS(ctx, "padmin", "alice", "", validReq, ""); !errors.Is(err, ErrReadOnlyStore) {
		t.Errorf("read only store want ErrReadOnlyStore, got %v", err)
	}
}

func TestGetUserQOS_HappyPath_And_Parsing(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		if strings.Contains(cmd, "where user=alice") {
			return []byte("alice|hpc-lab|normal,gpu-vip,high|gpu-vip\n"), nil
		}
		if strings.Contains(cmd, "where user=bob") {
			// Multi-row and delimiter variants
			return []byte("User|Account|QOS|DefQOS\nbob|bob|normal|normal\nbob|hpc-lab|normal + gpu-vip|gpu-vip\n"), nil
		}
		if strings.Contains(cmd, "where user=empty_qos") {
			return []byte("empty_qos|hpc-lab||\n"), nil
		}
		return []byte(""), nil
	})
	ctx := context.Background()

	_, _ = st.CreateTenant(ctx, "hpc-lab", "")
	_, _ = st.CreateUser(ctx, store.NewUser{Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab"})
	_, _ = st.CreateUser(ctx, store.NewUser{Username: "bob", Password: "bob1234567", Role: auth.RoleMember, TenantSlug: "hpc-lab"})
	_, _ = st.CreateUser(ctx, store.NewUser{Username: "empty_qos", Password: "empty12345", Role: auth.RoleMember, TenantSlug: "hpc-lab"})

	// 1. alice standard
	info, err := s.GetUserQOS(ctx, "alice", "")
	if err != nil {
		t.Fatalf("GetUserQOS alice: %v", err)
	}
	if info.Username != "alice" || info.DefaultQOS != "gpu-vip" || len(info.AllowedQOS) != 3 {
		t.Errorf("alice QOS info mismatch: %+v", info)
	}

	// 2. bob multi-row match
	infoBob, err := s.GetUserQOS(ctx, "bob", "hpc-lab")
	if err != nil {
		t.Fatalf("GetUserQOS bob: %v", err)
	}
	if infoBob.DefaultQOS != "gpu-vip" || len(infoBob.AllowedQOS) != 2 {
		t.Errorf("bob QOS info mismatch: %+v", infoBob)
	}

	// 3. empty QOS fallback to normal
	infoEmpty, err := s.GetUserQOS(ctx, "empty_qos", "")
	if err != nil {
		t.Fatalf("GetUserQOS empty: %v", err)
	}
	if infoEmpty.DefaultQOS != "normal" || len(infoEmpty.AllowedQOS) != 1 || infoEmpty.AllowedQOS[0] != "normal" {
		t.Errorf("empty_qos fallback mismatch: %+v", infoEmpty)
	}
}

func TestGetAvailableQOS_HappyPath_And_Fallback(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		if strings.Contains(cmd, "show assoc") {
			return []byte("alice|hpc-lab|normal,gpu-vip|gpu-vip\n"), nil
		}
		if strings.Contains(cmd, "show qos") {
			return []byte("Name|Priority|GrpTRES|MaxTRESPU|MaxWall|MaxJobsPU|MaxSubmitPU|Description\n" +
				"normal|0||||||Standard Default QOS\n" +
				"gpu-vip|1000|gres/gpu=4,cpu=32|gres/gpu=1,cpu=8|02:00:00|1|5|VIP Dedicated QOS\n"), nil
		}
		return []byte(""), nil
	})
	ctx := context.Background()

	_, _ = st.CreateTenant(ctx, "hpc-lab", "")
	_, _ = st.CreateUser(ctx, store.NewUser{Username: "alice", Password: "alice12345", Role: auth.RoleMember, TenantSlug: "hpc-lab"})

	resp, err := s.GetAvailableQOS(ctx, "alice", "")
	if err != nil {
		t.Fatalf("GetAvailableQOS alice: %v", err)
	}
	if resp.DefaultQOS != "gpu-vip" {
		t.Errorf("defaultQOS = %q, want gpu-vip", resp.DefaultQOS)
	}
	if len(resp.AllowedQOS) != 2 {
		t.Fatalf("allowedQOS len = %d, want 2", len(resp.AllowedQOS))
	}
	vip := resp.AllowedQOS[1]
	if vip.Name != "gpu-vip" || vip.Priority != "1000" || vip.MaxTRESPerUser != "gres/gpu=1,cpu=8" || vip.MaxJobsPerUser != "1" {
		t.Errorf("vip QOS object incomplete: %+v", vip)
	}
}
