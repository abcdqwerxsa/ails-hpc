package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"ails-hpc/pkg/store"
	_ "modernc.org/sqlite"
)

// TestChallenger_ParseQOSList_HeaderPermutations 测试不同表头组合与列顺序下的解析鲁棒性
func TestChallenger_ParseQOSList_HeaderPermutations(t *testing.T) {
	cases := []struct {
		name     string
		rawOut   string
		expected QOS
	}{
		{
			name: "Standard column order",
			rawOut: "Name|Priority|GrpTRES|MaxTRESPU|MaxJobsPU|MaxSubmitPU|MaxWall|Description\n" +
				"test-qos|500|cpu=32,mem=64G|gres/gpu=2,cpu=8|10|20|04:00:00|Standard QOS Test\n",
			expected: QOS{
				Name:                 "test-qos",
				Priority:             "500",
				GrpTRES:              "cpu=32,mem=64G",
				MaxTRESPerUser:       "gres/gpu=2,cpu=8",
				MaxTRES:              "gres/gpu=2,cpu=8",
				MaxJobsPerUser:       "10",
				MaxJobs:              "10",
				MaxSubmitJobsPerUser: "20",
				MaxWallDuration:      "04:00:00",
				MaxWall:              "04:00:00",
				Description:          "Standard QOS Test",
			},
		},
		{
			name: "Reversed column order (Priority, Description, MaxWall, MaxSubmitPU, MaxJobsPU, MaxTRESPU, GrpTRES, Name)",
			rawOut: "Priority|Description|MaxWall|MaxSubmitPU|MaxJobsPU|MaxTRESPU|GrpTRES|Name|\n" +
				"750|Reversed Cols|08:00:00|15|5|gres/gpu=1|gres/gpu=4|rev-qos|\n",
			expected: QOS{
				Name:                 "rev-qos",
				Priority:             "750",
				GrpTRES:              "gres/gpu=4",
				MaxTRESPerUser:       "gres/gpu=1",
				MaxTRES:              "gres/gpu=1",
				MaxJobsPerUser:       "5",
				MaxJobs:              "5",
				MaxSubmitJobsPerUser: "15",
				MaxWallDuration:      "08:00:00",
				MaxWall:              "08:00:00",
				Description:          "Reversed Cols",
			},
		},
		{
			name: "Alternative header aliases (MaxTRESPerUser, MaxJobsPerUser, MaxSubmitJobsPerUser, MaxWallDuration, Descr)",
			rawOut: "Name|Priority|GrpTRES|MaxTRESPerUser|MaxJobsPerUser|MaxSubmitJobsPerUser|MaxWallDuration|Descr\n" +
				"alias-qos|100|cpu=16|cpu=4|2|4|01:30:00|Alias Test\n",
			expected: QOS{
				Name:                 "alias-qos",
				Priority:             "100",
				GrpTRES:              "cpu=16",
				MaxTRESPerUser:       "cpu=4",
				MaxTRES:              "cpu=4",
				MaxJobsPerUser:       "2",
				MaxJobs:              "2",
				MaxSubmitJobsPerUser: "4",
				MaxWallDuration:      "01:30:00",
				MaxWall:              "01:30:00",
				Description:          "Alias Test",
			},
		},
		{
			name: "Older header aliases (GrpJobs, MaxTRESPJ, MaxWallPJ)",
			rawOut: "Name|Priority|GrpTRES|MaxTRESPJ|GrpJobs|MaxWallPJ\n" +
				"old-alias|300|gres/gpu=8|gres/gpu=2|5|1-00:00:00\n",
			expected: QOS{
				Name:            "old-alias",
				Priority:        "300",
				GrpTRES:         "gres/gpu=8",
				MaxTRES:         "gres/gpu=2",
				MaxTRESPerUser:  "gres/gpu=2",
				MaxJobs:         "5",
				MaxJobsPerUser:  "5",
				MaxWall:         "1-00:00:00",
				MaxWallDuration: "1-00:00:00",
			},
		},
		{
			name: "Mixed case headers with extra whitespaces",
			rawOut: "  NAME | priority | GrpTRES | MAXTRESPU | MaxJobsPU | MAXSUBMITPU | MAXWALL | description  \n" +
				"  mix-case  |  999  |  gres/gpu=2  |  gres/gpu=1  |  1  |  2  |  00:45:00  |  Case and Space test  \n",
			expected: QOS{
				Name:                 "mix-case",
				Priority:             "999",
				GrpTRES:              "gres/gpu=2",
				MaxTRESPerUser:       "gres/gpu=1",
				MaxTRES:              "gres/gpu=1",
				MaxJobsPerUser:       "1",
				MaxJobs:              "1",
				MaxSubmitJobsPerUser: "2",
				MaxWallDuration:      "00:45:00",
				MaxWall:              "00:45:00",
				Description:          "Case and Space test",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseQOSList(tc.rawOut)
			if len(got) != 1 {
				t.Fatalf("expected 1 item, got %d", len(got))
			}
			q := got[0]
			if q.Name != tc.expected.Name {
				t.Errorf("Name: got %q, want %q", q.Name, tc.expected.Name)
			}
			if q.Priority != tc.expected.Priority {
				t.Errorf("Priority: got %q, want %q", q.Priority, tc.expected.Priority)
			}
			if q.GrpTRES != tc.expected.GrpTRES {
				t.Errorf("GrpTRES: got %q, want %q", q.GrpTRES, tc.expected.GrpTRES)
			}
			if q.MaxTRESPerUser != tc.expected.MaxTRESPerUser {
				t.Errorf("MaxTRESPerUser: got %q, want %q", q.MaxTRESPerUser, tc.expected.MaxTRESPerUser)
			}
			if q.MaxJobsPerUser != tc.expected.MaxJobsPerUser {
				t.Errorf("MaxJobsPerUser: got %q, want %q", q.MaxJobsPerUser, tc.expected.MaxJobsPerUser)
			}
			if q.MaxSubmitJobsPerUser != tc.expected.MaxSubmitJobsPerUser {
				t.Errorf("MaxSubmitJobsPerUser: got %q, want %q", q.MaxSubmitJobsPerUser, tc.expected.MaxSubmitJobsPerUser)
			}
			if q.MaxWallDuration != tc.expected.MaxWallDuration {
				t.Errorf("MaxWallDuration: got %q, want %q", q.MaxWallDuration, tc.expected.MaxWallDuration)
			}
			if tc.expected.Description != "" && q.Description != tc.expected.Description {
				t.Errorf("Description: got %q, want %q", q.Description, tc.expected.Description)
			}
		})
	}
}

// TestChallenger_ParseQOSList_EdgeCases 测试极限与异常 Slurm 输出解析
func TestChallenger_ParseQOSList_EdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		rawOut   string
		wantLen  int
		validate func(t *testing.T, res []QOS)
	}{
		{
			name:    "Empty string",
			rawOut:  "",
			wantLen: 0,
		},
		{
			name:    "Only newlines and spaces",
			rawOut:  "   \n\n\t  \n   ",
			wantLen: 0,
		},
		{
			name:    "UNLIMITED values in all fields",
			rawOut:  "unlimited-qos|0|UNLIMITED|UNLIMITED|UNLIMITED|UNLIMITED|UNLIMITED|Full Unlimited\n",
			wantLen: 1,
			validate: func(t *testing.T, res []QOS) {
				q := res[0]
				if q.Name != "unlimited-qos" {
					t.Errorf("Name: got %q", q.Name)
				}
				if q.MaxJobsPerUser != "UNLIMITED" {
					t.Errorf("MaxJobsPerUser: got %q", q.MaxJobsPerUser)
				}
				if q.MaxWallDuration != "UNLIMITED" {
					t.Errorf("MaxWallDuration: got %q", q.MaxWallDuration)
				}
				if q.Description != "Full Unlimited" {
					t.Errorf("Description: got %q", q.Description)
				}
			},
		},
		{
			name:    "All fields empty except Name and Priority",
			rawOut:  "empty-fields|0|||||||\n",
			wantLen: 1,
			validate: func(t *testing.T, res []QOS) {
				q := res[0]
				if q.Name != "empty-fields" || q.Priority != "0" {
					t.Errorf("got %+v", q)
				}
				if q.GrpTRES != "" || q.MaxTRESPerUser != "" || q.MaxJobsPerUser != "" || q.MaxWall != "" {
					t.Errorf("expected empty limits, got %+v", q)
				}
			},
		},
		{
			name: "Corrupted and noisy lines interspersed with valid lines",
			rawOut: "sacctmgr: Warning: cluster unreachable initially\n" +
				"some random log line\n" +
				"|||||\n" +
				"|0|cpu=1\n" +
				"valid-1|100|cpu=4|cpu=1|00:30:00|1|2|Valid One\n" +
				"corrupted;line,no,pipes\n" +
				"valid-2|200||||||\n" +
				"\n",
			wantLen: 2,
			validate: func(t *testing.T, res []QOS) {
				if res[0].Name != "valid-1" || res[0].Priority != "100" || res[0].Description != "Valid One" {
					t.Errorf("res[0] mismatch: %+v", res[0])
				}
				if res[1].Name != "valid-2" || res[1].Priority != "200" {
					t.Errorf("res[1] mismatch: %+v", res[1])
				}
			},
		},
		{
			name: "Special characters in Description (Chinese, punctuation, brackets)",
			rawOut: "Name|Priority|GrpTRES|MaxTRESPU|MaxJobsPU|MaxSubmitPU|MaxWall|Description\n" +
				"special-desc|100|cpu=16|cpu=4|1|2|01:00:00|测试 QOS [高优先级] (GPU/V100:1) / 限时+限卡\n",
			wantLen: 1,
			validate: func(t *testing.T, res []QOS) {
				q := res[0]
				wantDesc := "测试 QOS [高优先级] (GPU/V100:1) / 限时+限卡"
				if q.Description != wantDesc {
					t.Errorf("Description: got %q, want %q", q.Description, wantDesc)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseQOSList(tc.rawOut)
			if len(got) != tc.wantLen {
				t.Fatalf("expected len %d, got %d (%+v)", tc.wantLen, len(got), got)
			}
			if tc.validate != nil {
				tc.validate(t, got)
			}
		})
	}
}

// TestChallenger_ParseQOSList_CRLFAndLargeScale 测试 Windows 换行符 CRLF 及千条 QOS 大规模解析
func TestChallenger_ParseQOSList_CRLFAndLargeScale(t *testing.T) {
	// 1. CRLF 换行符测试
	crlfOut := "Name|Priority|GrpTRES|MaxTRESPU|MaxJobsPU|MaxSubmitPU|MaxWall|Description\r\n" +
		"crlf-1|100|cpu=16|cpu=4|1|2|01:00:00|CRLF Test 1\r\n" +
		"crlf-2|200|gres/gpu=4|gres/gpu=1|2|4|02:00:00|CRLF Test 2\r\n"

	got := ParseQOSList(crlfOut)
	if len(got) != 2 {
		t.Fatalf("CRLF ParseQOSList want 2 got %d", len(got))
	}
	if got[0].Name != "crlf-1" || got[0].Description != "CRLF Test 1" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Name != "crlf-2" || got[1].MaxTRESPerUser != "gres/gpu=1" {
		t.Errorf("got[1] = %+v", got[1])
	}

	// 2. 1000 条 QOS 大规模生成解析测试
	var sb strings.Builder
	sb.WriteString("Name|Priority|GrpTRES|MaxTRESPU|MaxJobsPU|MaxSubmitPU|MaxWall|Description\n")
	for i := 1; i <= 1000; i++ {
		sb.WriteString(fmt.Sprintf("qos-scale-%04d|%d|cpu=%d|cpu=%d|%d|%d|%02d:00:00|Scale QOS %d\n",
			i, i*10, i*8, i*2, i%10+1, (i%10+1)*2, (i%24)+1, i))
	}

	scaleGot := ParseQOSList(sb.String())
	if len(scaleGot) != 1000 {
		t.Fatalf("scale test want 1000 items, got %d", len(scaleGot))
	}
	if scaleGot[999].Name != "qos-scale-1000" || scaleGot[999].Priority != "10000" {
		t.Errorf("scaleGot[999] = %+v", scaleGot[999])
	}
}

// TestChallenger_UpdateQOS_MultiFieldCombinations 测试多字段任意组合更新时的命令生成准确性
func TestChallenger_UpdateQOS_MultiFieldCombinations(t *testing.T) {
	combos := []struct {
		name       string
		updates    QOSUpdates
		wantTokens []string
	}{
		{
			name: "3 fields: Priority, GrpTRES, MaxWallDuration",
			updates: QOSUpdates{
				Priority:        "1200",
				GrpTRES:         "gres/gpu=8",
				MaxWallDuration: "08:00:00",
			},
			wantTokens: []string{
				"'Priority=1200'",
				"'GrpTRES=gres/gpu=8'",
				"'MaxWallDuration=08:00:00'",
			},
		},
		{
			name: "4 fields: MaxTRESPerUser, MaxJobsPerUser, MaxSubmitJobsPerUser, Description",
			updates: QOSUpdates{
				MaxTRESPerUser:       "gres/gpu=1,cpu=4",
				MaxJobsPerUser:       "2",
				MaxSubmitJobsPerUser: "5",
				Description:          "Updated limits and description",
			},
			wantTokens: []string{
				"'MaxTRESPerUser=gres/gpu=1,cpu=4'",
				"'MaxJobsPerUser=2'",
				"'MaxSubmitJobsPerUser=5'",
				"'Description=Updated limits and description'",
			},
		},
		{
			name: "All 7 fields full update",
			updates: QOSUpdates{
				Priority:             "9999",
				GrpTRES:              "gres/gpu=16,cpu=128",
				MaxTRESPerUser:       "gres/gpu=4,cpu=32",
				MaxJobsPerUser:       "10",
				MaxSubmitJobsPerUser: "20",
				MaxWallDuration:      "2-00:00:00",
				Description:          "Full 7 fields update",
			},
			wantTokens: []string{
				"'Priority=9999'",
				"'GrpTRES=gres/gpu=16,cpu=128'",
				"'MaxTRESPerUser=gres/gpu=4,cpu=32'",
				"'MaxJobsPerUser=10'",
				"'MaxSubmitJobsPerUser=20'",
				"'MaxWallDuration=2-00:00:00'",
				"'Description=Full 7 fields update'",
			},
		},
	}

	for _, tc := range combos {
		t.Run(tc.name, func(t *testing.T) {
			var capturedArgs []string
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				capturedArgs = args
				return []byte(""), nil
			})

			err := s.UpdateQOS(context.Background(), "padmin", "combo-qos", tc.updates, "rid-combo")
			if err != nil {
				t.Fatalf("UpdateQOS failed: %v", err)
			}

			cmdStr := strings.Join(capturedArgs, " ")
			for _, token := range tc.wantTokens {
				if !strings.Contains(cmdStr, token) {
					t.Errorf("command missing token %q: full cmd = %q", token, cmdStr)
				}
			}
		})
	}
}

// TestChallenger_UpdateQOS_SingleFieldPartialUpdates 验证单字段精准局部更新（10个独立字段逐一测试）
func TestChallenger_UpdateQOS_SingleFieldPartialUpdates(t *testing.T) {
	testFields := []struct {
		fieldName    string
		updates      QOSUpdates
		wantToken    string
		unwantTokens []string
	}{
		{
			fieldName:    "Description only",
			updates:      QOSUpdates{Description: "Updated desc only"},
			wantToken:    "'Description=Updated desc only'",
			unwantTokens: []string{"Priority=", "GrpTRES=", "MaxTRES=", "MaxJobs=", "MaxWall="},
		},
		{
			fieldName:    "Priority only",
			updates:      QOSUpdates{Priority: "888"},
			wantToken:    "'Priority=888'",
			unwantTokens: []string{"Description=", "GrpTRES=", "MaxTRES=", "MaxJobs=", "MaxWall="},
		},
		{
			fieldName:    "GrpTRES only",
			updates:      QOSUpdates{GrpTRES: "gres/gpu=8,cpu=64"},
			wantToken:    "'GrpTRES=gres/gpu=8,cpu=64'",
			unwantTokens: []string{"Priority=", "Description=", "MaxTRESPerUser=", "MaxJobsPerUser=", "MaxWall="},
		},
		{
			fieldName:    "MaxTRESPerUser only",
			updates:      QOSUpdates{MaxTRESPerUser: "gres/gpu=2,cpu=16"},
			wantToken:    "'MaxTRESPerUser=gres/gpu=2,cpu=16'",
			unwantTokens: []string{"Priority=", "Description=", "GrpTRES=", "MaxJobsPerUser=", "MaxWallDuration="},
		},
		{
			fieldName:    "MaxTRES alias only",
			updates:      QOSUpdates{MaxTRES: "gres/gpu=1"},
			wantToken:    "'MaxTRESPerUser=gres/gpu=1'",
			unwantTokens: []string{"Priority=", "Description=", "GrpTRES=", "MaxWall="},
		},
		{
			fieldName:    "MaxJobsPerUser only",
			updates:      QOSUpdates{MaxJobsPerUser: "3"},
			wantToken:    "'MaxJobsPerUser=3'",
			unwantTokens: []string{"Priority=", "Description=", "GrpTRES=", "MaxWall="},
		},
		{
			fieldName:    "MaxJobs alias only",
			updates:      QOSUpdates{MaxJobs: "5"},
			wantToken:    "'MaxJobsPerUser=5'",
			unwantTokens: []string{"Priority=", "Description=", "GrpTRES=", "MaxWall="},
		},
		{
			fieldName:    "MaxSubmitJobsPerUser only",
			updates:      QOSUpdates{MaxSubmitJobsPerUser: "12"},
			wantToken:    "'MaxSubmitJobsPerUser=12'",
			unwantTokens: []string{"Priority=", "Description=", "MaxJobsPerUser=", "MaxWall="},
		},
		{
			fieldName:    "MaxWallDuration only",
			updates:      QOSUpdates{MaxWallDuration: "06:30:00"},
			wantToken:    "'MaxWallDuration=06:30:00'",
			unwantTokens: []string{"Priority=", "Description=", "GrpTRES=", "MaxJobsPerUser="},
		},
		{
			fieldName:    "MaxWall alias only",
			updates:      QOSUpdates{MaxWall: "1-00:00:00"},
			wantToken:    "'MaxWallDuration=1-00:00:00'",
			unwantTokens: []string{"Priority=", "Description=", "GrpTRES=", "MaxJobsPerUser="},
		},
		{
			fieldName:    "UNLIMITED reset for MaxWallDuration",
			updates:      QOSUpdates{MaxWallDuration: "UNLIMITED"},
			wantToken:    "'MaxWallDuration=UNLIMITED'",
			unwantTokens: []string{"Priority=", "Description="},
		},
		{
			fieldName:    "-1 reset for Priority",
			updates:      QOSUpdates{Priority: "-1"},
			wantToken:    "'Priority=-1'",
			unwantTokens: []string{"Description=", "GrpTRES="},
		},
	}

	for _, tc := range testFields {
		t.Run(tc.fieldName, func(t *testing.T) {
			var capturedArgs []string
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				capturedArgs = args
				return []byte(""), nil
			})

			err := s.UpdateQOS(context.Background(), "padmin", "target-qos", tc.updates, "rid-partial-test")
			if err != nil {
				t.Fatalf("UpdateQOS failed: %v", err)
			}

			cmdStr := strings.Join(capturedArgs, " ")
			if !strings.HasPrefix(cmdStr, "sh -c sacctmgr -i modify qos target-qos set ") {
				t.Errorf("command prefix invalid: got %q", cmdStr)
			}
			if !strings.Contains(cmdStr, tc.wantToken) {
				t.Errorf("command missing expected token %q: full cmd = %q", tc.wantToken, cmdStr)
			}
			for _, unwant := range tc.unwantTokens {
				if strings.Contains(cmdStr, unwant) {
					t.Errorf("command unexpectedly contains untouched field token %q: full cmd = %q", unwant, cmdStr)
				}
			}
		})
	}
}

// TestChallenger_UpdateQOS_JSONUnmarshal_Aliases 测试 JSON 请求体反序列化对各种 camelCase / snake_case 别名支持
func TestChallenger_UpdateQOS_JSONUnmarshal_Aliases(t *testing.T) {
	testJSONs := []struct {
		name      string
		rawJSON   string
		assertVal func(t *testing.T, u QOSUpdates)
	}{
		{
			name:    "snake_case aliases",
			rawJSON: `{"grp_tres":"cpu=16","max_tres_pu":"gres/gpu=2","max_jobs_pu":"4","max_submit_pu":"8","max_wall_duration":"02:00:00"}`,
			assertVal: func(t *testing.T, u QOSUpdates) {
				if u.GrpTRES != "cpu=16" {
					t.Errorf("GrpTRES: got %q", u.GrpTRES)
				}
				if u.MaxTRESPerUser != "gres/gpu=2" {
					t.Errorf("MaxTRESPerUser: got %q", u.MaxTRESPerUser)
				}
				if u.MaxJobsPerUser != "4" {
					t.Errorf("MaxJobsPerUser: got %q", u.MaxJobsPerUser)
				}
				if u.MaxSubmitJobsPerUser != "8" {
					t.Errorf("MaxSubmitJobsPerUser: got %q", u.MaxSubmitJobsPerUser)
				}
				if u.MaxWallDuration != "02:00:00" {
					t.Errorf("MaxWallDuration: got %q", u.MaxWallDuration)
				}
			},
		},
		{
			name:    "abbreviated aliases (max_tres, max_jobs, max_wall)",
			rawJSON: `{"max_tres":"gres/gpu=1","max_jobs":"1","max_wall":"01:00:00"}`,
			assertVal: func(t *testing.T, u QOSUpdates) {
				if u.MaxTRESPerUser != "gres/gpu=1" || u.MaxTRES != "gres/gpu=1" {
					t.Errorf("MaxTRESPerUser: got %q", u.MaxTRESPerUser)
				}
				if u.MaxJobsPerUser != "1" || u.MaxJobs != "1" {
					t.Errorf("MaxJobsPerUser: got %q", u.MaxJobsPerUser)
				}
				if u.MaxWallDuration != "01:00:00" || u.MaxWall != "01:00:00" {
					t.Errorf("MaxWallDuration: got %q", u.MaxWallDuration)
				}
			},
		},
		{
			name:    "mixed camelCase and full field names",
			rawJSON: `{"description":"Mixed Test","priority":"900","maxTRESPerUser":"cpu=8","maxSubmitJobsPerUser":"10"}`,
			assertVal: func(t *testing.T, u QOSUpdates) {
				if u.Description != "Mixed Test" || u.Priority != "900" {
					t.Errorf("desc/prio mismatch: %+v", u)
				}
				if u.MaxTRESPerUser != "cpu=8" || u.MaxSubmitJobsPerUser != "10" {
					t.Errorf("limits mismatch: %+v", u)
				}
			},
		},
	}

	for _, tc := range testJSONs {
		t.Run(tc.name, func(t *testing.T) {
			var u QOSUpdates
			err := json.Unmarshal([]byte(tc.rawJSON), &u)
			if err != nil {
				t.Fatalf("json.Unmarshal failed: %v", err)
			}
			tc.assertVal(t, u)
		})
	}
}

// TestChallenger_AuditLog_DirectSQLiteVerification 直接查询 SQLite 底层 audit_log 表验证审计记录完整性与 JSON 正确性
func TestChallenger_AuditLog_DirectSQLiteVerification(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "challenger_audit_test.db")
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

	// 1. Create QOS
	createUpdates := QOSUpdates{
		Description:          "Audit Benchmark QOS",
		Priority:             "650",
		GrpTRES:              "gres/gpu=8,cpu=64",
		MaxTRESPerUser:       "gres/gpu=2,cpu=16",
		MaxJobsPerUser:       "3",
		MaxSubmitJobsPerUser: "6",
		MaxWallDuration:      "03:00:00",
	}
	_, err = s.CreateQOS(ctx, "auditor_user", "bench-qos", createUpdates, "req-audit-001")
	if err != nil {
		t.Fatalf("CreateQOS: %v", err)
	}

	// 2. Partial Update QOS (only Priority & MaxJobsPerUser)
	partialUpdates := QOSUpdates{
		Priority:       "800",
		MaxJobsPerUser: "5",
	}
	err = s.UpdateQOS(ctx, "auditor_user", "bench-qos", partialUpdates, "req-audit-002")
	if err != nil {
		t.Fatalf("UpdateQOS: %v", err)
	}

	// 3. Delete QOS
	err = s.DeleteQOS(ctx, "auditor_user", "bench-qos", "req-audit-003")
	if err != nil {
		t.Fatalf("DeleteQOS: %v", err)
	}

	// 直接通过 database/sql 连接底层 SQLite 数据库验证
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("direct sqlite open: %v", err)
	}
	defer db.Close()

	type rawAuditRow struct {
		ID        int64
		Actor     string
		Action    string
		Target    string
		RequestID string
		Detail    string
		CreatedAt string
	}

	rows, err := db.QueryContext(ctx, "SELECT id, actor, action, target, request_id, detail, created_at FROM audit_log ORDER BY id ASC")
	if err != nil {
		t.Fatalf("select audit_log: %v", err)
	}
	defer rows.Close()

	var auditRows []rawAuditRow
	for rows.Next() {
		var r rawAuditRow
		if err := rows.Scan(&r.ID, &r.Actor, &r.Action, &r.Target, &r.RequestID, &r.Detail, &r.CreatedAt); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		auditRows = append(auditRows, r)
	}

	if len(auditRows) != 3 {
		t.Fatalf("expected 3 audit rows in SQLite, found %d: %+v", len(auditRows), auditRows)
	}

	// 校验 Row 1: qos.create
	r1 := auditRows[0]
	if r1.Actor != "auditor_user" || r1.Action != "qos.create" || r1.Target != "qos:bench-qos" || r1.RequestID != "req-audit-001" {
		t.Errorf("row 1 mismatch: %+v", r1)
	}
	var r1Detail map[string]any
	if err := json.Unmarshal([]byte(r1.Detail), &r1Detail); err != nil {
		t.Fatalf("row 1 detail is not valid JSON (%q): %v", r1.Detail, err)
	}
	if r1Detail["priority"] != "650" || r1Detail["description"] != "Audit Benchmark QOS" || r1Detail["grpTRES"] != "gres/gpu=8,cpu=64" {
		t.Errorf("row 1 detail payload mismatch: %+v", r1Detail)
	}

	// 校验 Row 2: qos.modify (partial update)
	r2 := auditRows[1]
	if r2.Actor != "auditor_user" || r2.Action != "qos.modify" || r2.Target != "qos:bench-qos" || r2.RequestID != "req-audit-002" {
		t.Errorf("row 2 mismatch: %+v", r2)
	}
	var r2Detail map[string]any
	if err := json.Unmarshal([]byte(r2.Detail), &r2Detail); err != nil {
		t.Fatalf("row 2 detail is not valid JSON (%q): %v", r2.Detail, err)
	}
	if r2Detail["priority"] != "800" || r2Detail["maxJobsPerUser"] != "5" {
		t.Errorf("row 2 detail payload mismatch: %+v", r2Detail)
	}

	// 校验 Row 3: qos.delete
	r3 := auditRows[2]
	if r3.Actor != "auditor_user" || r3.Action != "qos.delete" || r3.Target != "qos:bench-qos" || r3.RequestID != "req-audit-003" {
		t.Errorf("row 3 mismatch: %+v", r3)
	}
	if r3.Detail != "{}" {
		t.Errorf("row 3 detail want '{}', got %q", r3.Detail)
	}
}

// TestChallenger_ErrorPropagationAndSlurmExitCodes 验证 Slurm 返回非零或错误文本时的行为与错误传播
func TestChallenger_ErrorPropagationAndSlurmExitCodes(t *testing.T) {
	cases := []struct {
		name         string
		runnerOutput []byte
		runnerErr    error
		action       func(s *Service) error
		wantErr      error
		wantSubstr   string
	}{
		{
			name:         "sacctmgr non-zero exit code error",
			runnerOutput: []byte("sacctmgr: error: Connection to slurmdbd failed: Connection refused"),
			runnerErr:    errors.New("exit status 1"),
			action: func(s *Service) error {
				_, err := s.ListQOS(context.Background())
				return err
			},
			wantSubstr: "sacctmgr show qos",
		},
		{
			name:         "sacctmgr modify unknown qos (without error keyword) -> ErrQOSNotFound",
			runnerOutput: []byte(" Unknown QOS 'ghost-qos'"),
			runnerErr:    nil,
			action: func(s *Service) error {
				return s.UpdateQOS(context.Background(), "padmin", "ghost-qos", QOSUpdates{Priority: "100"}, "rid")
			},
			wantErr: ErrQOSNotFound,
		},
		{
			name:         "sacctmgr modify nothing modified -> ErrQOSNotFound",
			runnerOutput: []byte(" Nothing modified"),
			runnerErr:    nil,
			action: func(s *Service) error {
				return s.UpdateQOS(context.Background(), "padmin", "ghost-qos", QOSUpdates{Priority: "100"}, "rid")
			},
			wantErr: ErrQOSNotFound,
		},
		{
			name:         "sacctmgr delete nothing deleted -> ErrQOSNotFound",
			runnerOutput: []byte(" Nothing deleted"),
			runnerErr:    nil,
			action: func(s *Service) error {
				return s.DeleteQOS(context.Background(), "padmin", "ghost-qos", "rid")
			},
			wantErr: ErrQOSNotFound,
		},
		{
			name:         "sacctmgr delete error output",
			runnerOutput: []byte("sacctmgr: error: Problem deleting QOS: In use by active jobs"),
			runnerErr:    nil,
			action: func(s *Service) error {
				return s.DeleteQOS(context.Background(), "padmin", "in-use-qos", "rid")
			},
			wantSubstr: "Problem deleting QOS: In use by active jobs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
				return tc.runnerOutput, tc.runnerErr
			})

			err := tc.action(s)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("got error %v, want %v", err, tc.wantErr)
			}
			if tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}
