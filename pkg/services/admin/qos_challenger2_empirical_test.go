package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ails-hpc/pkg/store"
	_ "modernc.org/sqlite"
)

// TestChallenger2_SacctmgrErrorVariationsMatrix 验证 sacctmgr 各种输出变种的解析与哨兵错误映射
func TestChallenger2_SacctmgrErrorVariationsMatrix(t *testing.T) {
	// 1. NotFound 变体矩阵 -> 必须准确返回 ErrQOSNotFound
	notFoundOutputs := []struct {
		desc   string
		output string
	}{
		{"Slurm 21.08 Unknown QOS with colon and single quotes", "sacctmgr: error: Unknown QOS: 'ghost-qos'"},
		{"Slurm 20.11 Unknown QOS with single quotes", "sacctmgr: error: Unknown QOS 'ghost-qos'"},
		{"Slurm Unknown QOS with double quotes", "sacctmgr: error: Unknown QOS \"ghost-qos\""},
		{"Slurm Unknown QOS without quotes", "sacctmgr: error: Unknown QOS: ghost-qos"},
		{"Slurm Unknown QOS trailing message", "sacctmgr: error: Unknown QOS ghost-qos specified"},
		{"Slurm Unknown QOS bare prefix", "Unknown QOS"},
		{"Slurm lower case unknown qos", "sacctmgr: error: unknown qos 'ghost-qos'"},
		{"Slurm UPPER CASE UNKNOWN QOS", "SACCTMGR: ERROR: UNKNOWN QOS 'GHOST-QOS'"},
		{"Slurm standard Nothing modified leading space", " Nothing modified"},
		{"Slurm Nothing modified with newline and spaces", "   Nothing modified\n\n"},
		{"Slurm lowercase nothing modified", " nothing modified"},
		{"Slurm uppercase NOTHING MODIFIED", " NOTHING MODIFIED"},
		{"Slurm standard Nothing deleted leading space", " Nothing deleted"},
		{"Slurm Nothing deleted with newline and spaces", "   Nothing deleted\n\n"},
		{"Slurm lowercase nothing deleted", " nothing deleted"},
		{"Slurm uppercase NOTHING DELETED", " NOTHING DELETED"},
		{"sacctmgr error prefix before Nothing modified", "sacctmgr: error: Nothing modified"},
		{"sacctmgr error prefix before Nothing deleted", "sacctmgr: error: Nothing deleted"},
	}

	for _, tc := range notFoundOutputs {
		t.Run("UpdateQOS_NotFound_"+tc.desc, func(t *testing.T) {
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				return []byte(tc.output), nil
			})
			err := s.UpdateQOS(context.Background(), "padmin", "ghost-qos", QOSUpdates{Priority: "100"}, "rid-nf-test")
			if !errors.Is(err, ErrQOSNotFound) {
				t.Errorf("output %q: expected ErrQOSNotFound, got %v", tc.output, err)
			}
		})

		t.Run("DeleteQOS_NotFound_"+tc.desc, func(t *testing.T) {
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				return []byte(tc.output), nil
			})
			err := s.DeleteQOS(context.Background(), "padmin", "ghost-qos", "rid-nf-test")
			if !errors.Is(err, ErrQOSNotFound) {
				t.Errorf("output %q: expected ErrQOSNotFound, got %v", tc.output, err)
			}
		})
	}

	// 2. 非 NotFound 的真实 Slurm 集群错误 -> 必须返回常规 error，绝不可误判为 ErrQOSNotFound
	systemErrors := []struct {
		desc       string
		output     string
		runnerErr  error
		wantSubstr string
	}{
		{"SlurmDBD down", "sacctmgr: error: SlurmDBD is not responding", nil, "SlurmDBD is not responding"},
		{"Connection refused", "sacctmgr: error: slurm_persist_conn_open: Connection refused", nil, "Connection refused"},
		{"Problem connecting to server", "sacctmgr: error: Problem connecting to server", nil, "Problem connecting to server"},
		{"Permission denied", "sacctmgr: error: Access/permission denied for user padmin", nil, "Access/permission denied"},
		{"In use by active jobs", "sacctmgr: error: Problem deleting QOS: In use by running jobs", nil, "In use by running jobs"},
		{"Generic fatal error", "sacctmgr: fatal: slurmdbd communication timeout", errors.New("exit status 1"), "exit status 1"},
	}

	for _, tc := range systemErrors {
		t.Run("UpdateQOS_SystemError_"+tc.desc, func(t *testing.T) {
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				return []byte(tc.output), tc.runnerErr
			})
			err := s.UpdateQOS(context.Background(), "padmin", "test-qos", QOSUpdates{Priority: "100"}, "rid-sys-test")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if errors.Is(err, ErrQOSNotFound) {
				t.Errorf("system error %q should NOT be mapped to ErrQOSNotFound", tc.output)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q should contain substring %q", err.Error(), tc.wantSubstr)
			}
		})

		t.Run("DeleteQOS_SystemError_"+tc.desc, func(t *testing.T) {
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				return []byte(tc.output), tc.runnerErr
			})
			err := s.DeleteQOS(context.Background(), "padmin", "test-qos", "rid-sys-test")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if errors.Is(err, ErrQOSNotFound) {
				t.Errorf("system error %q should NOT be mapped to ErrQOSNotFound", tc.output)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q should contain substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestChallenger2_PartialAndFullUpdates 深度测试局部单字段更新、多字段组合更新及 Slurm 重置语法
func TestChallenger2_PartialAndFullUpdates(t *testing.T) {
	// 1. 逐一测试每个可更新字段的局部更新行为与命令组装
	singleFieldCases := []struct {
		name      string
		updates   QOSUpdates
		wantToken string
	}{
		{"Description", QOSUpdates{Description: "Single Desc Only"}, "'Description=Single Desc Only'"},
		{"Priority", QOSUpdates{Priority: "777"}, "'Priority=777'"},
		{"GrpTRES", QOSUpdates{GrpTRES: "cpu=64,mem=128G"}, "'GrpTRES=cpu=64,mem=128G'"},
		{"MaxTRESPerUser", QOSUpdates{MaxTRESPerUser: "gres/gpu=2,cpu=8"}, "'MaxTRESPerUser=gres/gpu=2,cpu=8'"},
		{"MaxTRES (alias for MaxTRESPerUser)", QOSUpdates{MaxTRES: "gres/gpu=4"}, "'MaxTRESPerUser=gres/gpu=4'"},
		{"MaxJobsPerUser", QOSUpdates{MaxJobsPerUser: "4"}, "'MaxJobsPerUser=4'"},
		{"MaxJobs (alias for MaxJobsPerUser)", QOSUpdates{MaxJobs: "8"}, "'MaxJobsPerUser=8'"},
		{"MaxSubmitJobsPerUser", QOSUpdates{MaxSubmitJobsPerUser: "16"}, "'MaxSubmitJobsPerUser=16'"},
		{"MaxWallDuration", QOSUpdates{MaxWallDuration: "12:00:00"}, "'MaxWallDuration=12:00:00'"},
		{"MaxWall (alias for MaxWallDuration)", QOSUpdates{MaxWall: "1-00:00:00"}, "'MaxWallDuration=1-00:00:00'"},
	}

	for _, tc := range singleFieldCases {
		t.Run("SingleField_"+tc.name, func(t *testing.T) {
			var cmdRan string
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				cmdRan = strings.Join(args, " ")
				return []byte(""), nil
			})

			err := s.UpdateQOS(context.Background(), "padmin", "my-qos", tc.updates, "rid-single")
			if err != nil {
				t.Fatalf("UpdateQOS: %v", err)
			}
			expectedPrefix := "sh -c sacctmgr -i modify qos my-qos set "
			if !strings.HasPrefix(cmdRan, expectedPrefix) {
				t.Errorf("command prefix invalid: %q", cmdRan)
			}
			if !strings.Contains(cmdRan, tc.wantToken) {
				t.Errorf("command missing %q: full cmd = %q", tc.wantToken, cmdRan)
			}
		})
	}

	// 2. Slurm 重置语法（UNLIMITED, -1）放行与生成测试
	resetCases := []struct {
		name       string
		updates    QOSUpdates
		wantTokens []string
	}{
		{
			name: "Reset Priority to -1",
			updates: QOSUpdates{
				Priority: "-1",
			},
			wantTokens: []string{"'Priority=-1'"},
		},
		{
			name: "Reset GrpTRES and MaxTRESPerUser to -1",
			updates: QOSUpdates{
				GrpTRES:        "-1",
				MaxTRESPerUser: "-1",
			},
			wantTokens: []string{"'GrpTRES=-1'", "'MaxTRESPerUser=-1'"},
		},
		{
			name: "Reset Jobs and Wall to UNLIMITED",
			updates: QOSUpdates{
				MaxJobsPerUser:       "UNLIMITED",
				MaxSubmitJobsPerUser: "UNLIMITED",
				MaxWallDuration:      "UNLIMITED",
			},
			wantTokens: []string{"'MaxJobsPerUser=UNLIMITED'", "'MaxSubmitJobsPerUser=UNLIMITED'", "'MaxWallDuration=UNLIMITED'"},
		},
		{
			name: "Reset Jobs and Wall to -1",
			updates: QOSUpdates{
				MaxJobsPerUser:       "-1",
				MaxSubmitJobsPerUser: "-1",
				MaxWallDuration:      "-1",
			},
			wantTokens: []string{"'MaxJobsPerUser=-1'", "'MaxSubmitJobsPerUser=-1'", "'MaxWallDuration=-1'"},
		},
	}

	for _, tc := range resetCases {
		t.Run("ResetSyntax_"+tc.name, func(t *testing.T) {
			var cmdRan string
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				cmdRan = strings.Join(args, " ")
				return []byte(""), nil
			})

			err := s.UpdateQOS(context.Background(), "padmin", "my-qos", tc.updates, "rid-reset")
			if err != nil {
				t.Fatalf("UpdateQOS reset: %v", err)
			}
			for _, token := range tc.wantTokens {
				if !strings.Contains(cmdRan, token) {
					t.Errorf("command missing %q: full cmd = %q", token, cmdRan)
				}
			}
		})
	}

	// 3. 全量 7 字段更新
	t.Run("Full7FieldsUpdate", func(t *testing.T) {
		var cmdRan string
		s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			cmdRan = strings.Join(args, " ")
			return []byte(""), nil
		})

		fullUpdates := QOSUpdates{
			Description:          "Complete 7 fields update test",
			Priority:             "5000",
			GrpTRES:              "cpu=128,mem=256G,gres/gpu=8",
			MaxTRESPerUser:       "cpu=16,mem=32G,gres/gpu=2",
			MaxJobsPerUser:       "5",
			MaxSubmitJobsPerUser: "15",
			MaxWallDuration:      "1-12:00:00",
		}

		err := s.UpdateQOS(context.Background(), "padmin", "full-qos", fullUpdates, "rid-full")
		if err != nil {
			t.Fatalf("UpdateQOS full: %v", err)
		}

		tokens := []string{
			"'Description=Complete 7 fields update test'",
			"'Priority=5000'",
			"'GrpTRES=cpu=128,mem=256G,gres/gpu=8'",
			"'MaxTRESPerUser=cpu=16,mem=32G,gres/gpu=2'",
			"'MaxJobsPerUser=5'",
			"'MaxSubmitJobsPerUser=15'",
			"'MaxWallDuration=1-12:00:00'",
		}
		for _, tok := range tokens {
			if !strings.Contains(cmdRan, tok) {
				t.Errorf("full update missing token %q in %q", tok, cmdRan)
			}
		}
	})
}

// TestChallenger2_ParsingRobustnessAndEdgeCases 验证解析器在各种畸变、多版本和边界格式下的鲁棒性
func TestChallenger2_ParsingRobustnessAndEdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantLen  int
		validate func(t *testing.T, res []QOS)
	}{
		{
			name: "Slurm 21.08 default format with Header",
			input: "Name|Priority|GrpTRES|MaxTRESPU|MaxWall|MaxJobsPU|MaxSubmitJobsPU|Description\n" +
				"normal|0||||||Standard Normal QOS\n" +
				"vip|1000|gres/gpu=8|gres/gpu=2|02:00:00|2|5|VIP Dedicated\n",
			wantLen: 2,
			validate: func(t *testing.T, res []QOS) {
				if res[0].Name != "normal" || res[0].Priority != "0" {
					t.Errorf("normal mismatch: %+v", res[0])
				}
				if res[1].Name != "vip" || res[1].MaxTRESPerUser != "gres/gpu=2" || res[1].MaxWallDuration != "02:00:00" {
					t.Errorf("vip mismatch: %+v", res[1])
				}
			},
		},
		{
			name: "Slurm 22.05 format with MaxTRES and MaxTRESPU columns",
			input: "Name|Priority|GrpTRES|MaxTRESPU|MaxTRES|MaxJobsPU|MaxSubmitPU|MaxWall|Description\n" +
				"gpu-share|500|gres/gpu=16|gres/gpu=1|gres/gpu=4|1|3|01:30:00|Share GPUs\n",
			wantLen: 1,
			validate: func(t *testing.T, res []QOS) {
				q := res[0]
				if q.Name != "gpu-share" || q.MaxTRESPerUser != "gres/gpu=1" || q.MaxTRES != "gres/gpu=4" {
					t.Errorf("gpu-share mismatch: %+v", q)
				}
				if q.MaxWall != "01:30:00" || q.MaxWallDuration != "01:30:00" {
					t.Errorf("gpu-share wall mismatch: %+v", q)
				}
			},
		},
		{
			name: "No header 7/8 positional fallback",
			input: "batch|100|cpu=32|cpu=8|04:00:00|4|8|Batch Jobs\n" +
				"debug|50|cpu=8|cpu=2|00:30:00|1|2|Debug Queue\n",
			wantLen: 2,
			validate: func(t *testing.T, res []QOS) {
				if res[0].Name != "batch" || res[0].MaxWallDuration != "04:00:00" || res[0].MaxJobsPerUser != "4" {
					t.Errorf("batch mismatch: %+v", res[0])
				}
				if res[1].Name != "debug" || res[1].MaxWallDuration != "00:30:00" || res[1].MaxJobsPerUser != "1" {
					t.Errorf("debug mismatch: %+v", res[1])
				}
			},
		},
		{
			name:    "No header 6 column legacy fallback",
			input:   "legacy-qos|200|gres/gpu=4|gres/gpu=1|01:00:00|2\n",
			wantLen: 1,
			validate: func(t *testing.T, res []QOS) {
				q := res[0]
				if q.Name != "legacy-qos" || q.Priority != "200" || q.MaxTRES != "gres/gpu=1" || q.MaxWall != "01:00:00" || q.MaxJobs != "2" {
					t.Errorf("legacy-qos mismatch: %+v", q)
				}
			},
		},
		{
			name:    "Multiple empty lines, spaces, and no-pipe lines",
			input:   "\n\n   \nsacctmgr: Warning: cluster sync ongoing\n\n\t\n",
			wantLen: 0,
		},
		{
			name: "Corrupted lines with pipe but empty Name",
			input: "|||||\n" +
				" | | | | | \n" +
				"good-qos|100||||||\n",
			wantLen: 1,
			validate: func(t *testing.T, res []QOS) {
				if res[0].Name != "good-qos" || res[0].Priority != "100" {
					t.Errorf("good-qos mismatch: %+v", res[0])
				}
			},
		},
		{
			name: "Chinese description and complex TRES with colons and slashes",
			input: "Name|Priority|GrpTRES|MaxTRESPU|MaxJobsPU|MaxSubmitPU|MaxWall|Description\n" +
				"ai-cluster|2000|gres/gpu:a100-sxm4=8,cpu=64,mem=256G|gres/gpu:a100-sxm4=2,cpu=16|2|4|7-00:00:00|智算中心 A100-SXM4 高性能训练队列 (限长7天)\n",
			wantLen: 1,
			validate: func(t *testing.T, res []QOS) {
				q := res[0]
				if q.Name != "ai-cluster" || q.Priority != "2000" {
					t.Errorf("name/prio mismatch: %+v", q)
				}
				if q.GrpTRES != "gres/gpu:a100-sxm4=8,cpu=64,mem=256G" {
					t.Errorf("GrpTRES mismatch: got %q", q.GrpTRES)
				}
				if q.MaxTRESPerUser != "gres/gpu:a100-sxm4=2,cpu=16" {
					t.Errorf("MaxTRESPerUser mismatch: got %q", q.MaxTRESPerUser)
				}
				if q.MaxWallDuration != "7-00:00:00" {
					t.Errorf("MaxWallDuration mismatch: got %q", q.MaxWallDuration)
				}
				if q.Description != "智算中心 A100-SXM4 高性能训练队列 (限长7天)" {
					t.Errorf("Description mismatch: got %q", q.Description)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseQOSList(tc.input)
			if len(got) != tc.wantLen {
				t.Fatalf("expected len %d, got %d (got: %+v)", tc.wantLen, len(got), got)
			}
			if tc.validate != nil {
				tc.validate(t, got)
			}
		})
	}
}

// TestChallenger2_AuditLoggingAndProtectedResources 验证审计日志完整性、正常操作与拒绝操作的审计行为
func TestChallenger2_AuditLoggingAndProtectedResources(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "challenger2_audit.db")
	stRaw, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = stRaw.Close() })
	st := stRaw.(store.AdminStore)

	s := NewService(st, nil)
	s.SetClusterRunner(func(args ...string) ([]byte, error) {
		return []byte(""), nil
	})
	ctx := context.Background()

	// 1. 创建租户以便测试 SetTenantQOS
	if _, err := st.CreateTenant(ctx, "ai-tenant", ""); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// 2. 正常 CRUD + SetTenantQOS
	cUpdates := QOSUpdates{
		Description:    "AI Lab QOS",
		Priority:       "900",
		GrpTRES:        "gres/gpu=8",
		MaxJobsPerUser: "2",
	}
	_, err = s.CreateQOS(ctx, "admin_user", "ai-qos", cUpdates, "req-audit-c")
	if err != nil {
		t.Fatalf("CreateQOS: %v", err)
	}

	pUpdates := QOSUpdates{
		Priority: "1200",
	}
	err = s.UpdateQOS(ctx, "admin_user", "ai-qos", pUpdates, "req-audit-u")
	if err != nil {
		t.Fatalf("UpdateQOS: %v", err)
	}

	err = s.SetTenantQOS(ctx, "admin_user", "ai-tenant", "ai-qos", "req-audit-t")
	if err != nil {
		t.Fatalf("SetTenantQOS: %v", err)
	}

	err = s.DeleteQOS(ctx, "admin_user", "ai-qos", "req-audit-d")
	if err != nil {
		t.Fatalf("DeleteQOS: %v", err)
	}

	// 3. 触发被拒绝的操作（非法校验、删除 normal）-> 绝不应写入审计表
	_ = s.UpdateQOS(ctx, "admin_user", "ai-qos", QOSUpdates{Priority: "-999"}, "req-reject-1")
	_ = s.DeleteQOS(ctx, "admin_user", "normal", "req-reject-2")
	_ = s.DeleteQOS(ctx, "admin_user", "NORMAL", "req-reject-3")

	// 4. 从底层 SQLite 读取并验证所有审计行
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, "SELECT actor, action, target, request_id, detail FROM audit_log ORDER BY id ASC")
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	defer rows.Close()

	type auditRecord struct {
		Actor     string
		Action    string
		Target    string
		RequestID string
		Detail    string
	}
	var records []auditRecord
	for rows.Next() {
		var r auditRecord
		if err := rows.Scan(&r.Actor, &r.Action, &r.Target, &r.RequestID, &r.Detail); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		records = append(records, r)
	}

	// 应恰好有 4 条审计记录 (qos.create, qos.modify, tenant.qos, qos.delete)
	if len(records) != 4 {
		t.Fatalf("expected exactly 4 audit records, got %d: %+v", len(records), records)
	}

	// 验证第 1 条: qos.create
	if records[0].Action != "qos.create" || records[0].Target != "qos:ai-qos" || records[0].RequestID != "req-audit-c" {
		t.Errorf("rec[0] mismatch: %+v", records[0])
	}
	var d0 map[string]any
	if err := json.Unmarshal([]byte(records[0].Detail), &d0); err != nil || d0["priority"] != "900" || d0["grpTRES"] != "gres/gpu=8" {
		t.Errorf("rec[0] detail mismatch: %q", records[0].Detail)
	}

	// 验证第 2 条: qos.modify
	if records[1].Action != "qos.modify" || records[1].Target != "qos:ai-qos" || records[1].RequestID != "req-audit-u" {
		t.Errorf("rec[1] mismatch: %+v", records[1])
	}
	var d1 map[string]any
	if err := json.Unmarshal([]byte(records[1].Detail), &d1); err != nil || d1["priority"] != "1200" {
		t.Errorf("rec[1] detail mismatch: %q", records[1].Detail)
	}

	// 验证第 3 条: tenant.qos
	if records[2].Action != "tenant.qos" || records[2].Target != "tenant:ai-tenant" || records[2].RequestID != "req-audit-t" {
		t.Errorf("rec[2] mismatch: %+v", records[2])
	}
	var d2 map[string]any
	if err := json.Unmarshal([]byte(records[2].Detail), &d2); err != nil || d2["qos"] != "ai-qos" {
		t.Errorf("rec[2] detail mismatch: %q", records[2].Detail)
	}

	// 验证第 4 条: qos.delete
	if records[3].Action != "qos.delete" || records[3].Target != "qos:ai-qos" || records[3].RequestID != "req-audit-d" {
		t.Errorf("rec[3] mismatch: %+v", records[3])
	}
	if records[3].Detail != "{}" {
		t.Errorf("rec[3] detail want '{}', got %q", records[3].Detail)
	}
}
