package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestAdversarial_QOSNameInjectionAndBoundaries 测试 QOS 名称的各种注入与边界
func TestAdversarial_QOSNameInjectionAndBoundaries(t *testing.T) {
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		t.Fatalf("runner MUST NOT be called for invalid QOS name: %v", args)
		return nil, nil
	})

	maliciousNames := []struct {
		name string
		desc string
	}{
		{"", "空字符串"},
		{"1qos", "以数字开头"},
		{"_qos", "以下划线开头"},
		{"-qos", "以中划线开头"},
		{".qos", "以点开头"},
		{"/qos", "以斜杠开头"},
		{"qos;rm -rf /", "分号命令注入"},
		{"qos' || id || '", "单引号逃逸"},
		{"qos\" || id || \"", "双引号逃逸"},
		{"qos`whoami`", "反引号命令替换"},
		{"qos$(whoami)", "$() 命令替换"},
		{"qos${IFS}run", "${IFS} 环境变量注入"},
		{"qos|cat /etc/shadow", "管道命令注入"},
		{"qos&bg_task", "后台任务 & 注入"},
		{"qos>out.txt", "重定向注入"},
		{"qos name", "包含空格"},
		{"qos\tname", "包含制表符"},
		{"qos\nname", "包含换行符"},
		{"qos\r\nname", "包含回车换行"},
		{"qos\x00inject", "包含 Null 字节"},
		{"高性能QOS", "非 ASCII 汉字"},
		{"qos🚀", "包含 Emoji"},
		{strings.Repeat("a", 33), "超长名称 (33 字符)"},
		{strings.Repeat("B", 100), "超长名称 (100 字符)"},
	}

	for _, tc := range maliciousNames {
		t.Run(tc.desc, func(t *testing.T) {
			// 1. GetQOS
			if _, err := s.GetQOS(context.Background(), tc.name); err == nil {
				t.Errorf("GetQOS(%q) expected validation error, got nil", tc.name)
			}
			// 2. CreateQOS
			if _, err := s.CreateQOS(context.Background(), "admin", tc.name, QOSUpdates{Priority: "100"}, "req-adv"); err == nil {
				t.Errorf("CreateQOS(%q) expected validation error, got nil", tc.name)
			}
			// 3. UpdateQOS
			if err := s.UpdateQOS(context.Background(), "admin", tc.name, QOSUpdates{Priority: "100"}, "req-adv"); err == nil {
				t.Errorf("UpdateQOS(%q) expected validation error, got nil", tc.name)
			}
			// 4. DeleteQOS
			if err := s.DeleteQOS(context.Background(), "admin", tc.name, "req-adv"); err == nil {
				t.Errorf("DeleteQOS(%q) expected validation error, got nil", tc.name)
			}
		})
	}

	// 合法边界测试
	validNames := []struct {
		name string
		desc string
	}{
		{"a", "单字符小写"},
		{"Z", "单字符大写"},
		{"gpu-vip", "标准连字符"},
		{"gpu_vip_2", "标准下划线与数字"},
		{"A" + strings.Repeat("b", 31), "精确 32 字符上限"},
	}

	for _, tc := range validNames {
		t.Run("Valid_"+tc.desc, func(t *testing.T) {
			if !qosNameRE.MatchString(tc.name) {
				t.Errorf("valid QOS name %q was rejected by qosNameRE", tc.name)
			}
		})
	}
}

// TestAdversarial_FieldInjectionPayloads 测试所有字段的恶意注入与参数校验
func TestAdversarial_FieldInjectionPayloads(t *testing.T) {
	s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
		t.Fatalf("runner MUST NOT be called when field validation fails: %v", args)
		return nil, nil
	})

	tests := []struct {
		field   string
		payload string
		valid   bool
		desc    string
	}{
		// --- Description ---
		{"Description", "Standard description with symbols: (GPU) [Queue] 2026/08/21 - normal + vip.", true, "标准英文及安全符号"},
		{"Description", "研发部-GPU通用队列 (限卡:1/限时:2小时)", true, "标准中文及安全符号"},
		{"Description", "Desc with ' single quote", false, "Description 单引号逃逸"},
		{"Description", "Desc with \" double quote", false, "Description 双引号逃逸"},
		{"Description", "Desc with `whoami`", false, "Description 反引号命令替换"},
		{"Description", "Desc with $(reboot)", false, "Description $() 命令替换"},
		{"Description", "Desc; rm -rf /", false, "Description 分号注入"},
		{"Description", "Desc | cat /etc/passwd", false, "Description 管道注入"},
		{"Description", "Desc > /tmp/hack", false, "Description 重定向注入"},
		{"Description", "Desc\nNewline", false, "Description 换行符注入"},
		{"Description", "Desc\x00NullByte", false, "Description Null 字节注入"},
		{"Description", "Desc with Emoji 🚀", false, "Description Emoji"},
		{"Description", strings.Repeat("A", 128), true, "Description 128 字符上限"},
		{"Description", strings.Repeat("A", 129), false, "Description 129 字符溢出"},

		// --- Priority ---
		{"Priority", "0", true, "Priority 0"},
		{"Priority", "100", true, "Priority 100"},
		{"Priority", "4294967295", true, "Priority uint32 上限"},
		{"Priority", "-1", true, "Priority -1 (Slurm 缺省/重置)"},
		{"Priority", "-2", false, "Priority 负数 -2"},
		{"Priority", "-100", false, "Priority 负数 -100"},
		{"Priority", "0100", false, "Priority 前导 0"},
		{"Priority", "100;reboot", false, "Priority 分号注入"},
		{"Priority", "100' OR '1'='1", false, "Priority 单引号注入"},
		{"Priority", "100$(id)", false, "Priority $() 注入"},
		{"Priority", "high", false, "Priority 非数字"},
		{"Priority", "1.5", false, "Priority 浮点数"},
		{"Priority", "100 200", false, "Priority 包含空格"},
		{"Priority", "99999999999", false, "Priority 11位数字 (超限)"},

		// --- GrpTRES ---
		{"GrpTRES", "cpu=16", true, "GrpTRES 单 CPU"},
		{"GrpTRES", "gres/gpu=4", true, "GrpTRES 单 GPU"},
		{"GrpTRES", "gres/gpu:a100=2", true, "GrpTRES 指定型号 GPU"},
		{"GrpTRES", "gres/gpu:a100-sxm4=8", true, "GrpTRES 复杂型号 GPU"},
		{"GrpTRES", "cpu=32,mem=64G,gres/gpu=4", true, "GrpTRES 多资源逗号组合"},
		{"GrpTRES", "cpu=32,mem=64g,node=2", true, "GrpTRES 小写单位"},
		{"GrpTRES", "mem=1024M,gres/gpu=1", true, "GrpTRES M 单位"},
		{"GrpTRES", "-1", true, "GrpTRES -1 (清除限额)"},
		{"GrpTRES", "cpu=16;rm -rf /", false, "GrpTRES 分号注入"},
		{"GrpTRES", "cpu=16' OR '1'='1", false, "GrpTRES 单引号注入"},
		{"GrpTRES", "cpu=16`id`", false, "GrpTRES 反引号注入"},
		{"GrpTRES", "cpu=16$(id)", false, "GrpTRES $() 注入"},
		{"GrpTRES", "cpu=16,gres/gpu=4;reboot", false, "GrpTRES 复合分号注入"},
		{"GrpTRES", "cpu=16,", false, "GrpTRES 尾随逗号"},
		{"GrpTRES", ",cpu=16", false, "GrpTRES 前导逗号"},
		{"GrpTRES", "cpu=-1", false, "GrpTRES 键值负数"},
		{"GrpTRES", "cpu=1.5", false, "GrpTRES 浮点数值"},
		{"GrpTRES", "cpu", false, "GrpTRES 缺少等号"},
		{"GrpTRES", "=16", false, "GrpTRES 缺少键名"},
		{"GrpTRES", "cpu=16X", false, "GrpTRES 非法单位 X"},
		{"GrpTRES", "cpu=16 mem=32", false, "GrpTRES 空格分隔而不是逗号"},

		// --- MaxTRESPerUser ---
		{"MaxTRESPerUser", "gres/gpu=1,cpu=8", true, "MaxTRESPerUser 正常配置"},
		{"MaxTRESPerUser", "-1", true, "MaxTRESPerUser -1"},
		{"MaxTRESPerUser", "gres/gpu=1;kill -9 1", false, "MaxTRESPerUser 分号注入"},
		{"MaxTRESPerUser", "gres/gpu=1' --", false, "MaxTRESPerUser 单引号逃逸"},

		// --- MaxJobsPerUser ---
		{"MaxJobsPerUser", "1", true, "MaxJobsPerUser 1"},
		{"MaxJobsPerUser", "0", true, "MaxJobsPerUser 0"},
		{"MaxJobsPerUser", "1000", true, "MaxJobsPerUser 1000"},
		{"MaxJobsPerUser", "UNLIMITED", true, "MaxJobsPerUser UNLIMITED"},
		{"MaxJobsPerUser", "unlimited", true, "MaxJobsPerUser unlimited 小写"},
		{"MaxJobsPerUser", "-1", true, "MaxJobsPerUser -1"},
		{"MaxJobsPerUser", "-2", false, "MaxJobsPerUser 负数 -2"},
		{"MaxJobsPerUser", "1;rm -rf /", false, "MaxJobsPerUser 分号注入"},
		{"MaxJobsPerUser", "1' || '1", false, "MaxJobsPerUser 单引号注入"},
		{"MaxJobsPerUser", "100000000", false, "MaxJobsPerUser 9位数字 (超 8 位限制)"},
		{"MaxJobsPerUser", "one", false, "MaxJobsPerUser 非数字"},

		// --- MaxSubmitJobsPerUser ---
		{"MaxSubmitJobsPerUser", "5", true, "MaxSubmitJobsPerUser 5"},
		{"MaxSubmitJobsPerUser", "UNLIMITED", true, "MaxSubmitJobsPerUser UNLIMITED"},
		{"MaxSubmitJobsPerUser", "5;reboot", false, "MaxSubmitJobsPerUser 分号注入"},

		// --- MaxWallDuration ---
		{"MaxWallDuration", "120", true, "MaxWallDuration 分钟整数"},
		{"MaxWallDuration", "02:00:00", true, "MaxWallDuration HH:MM:SS"},
		{"MaxWallDuration", "30:00", true, "MaxWallDuration MM:SS"},
		{"MaxWallDuration", "1-12:00:00", true, "MaxWallDuration D-HH:MM:SS"},
		{"MaxWallDuration", "7-00:00:00", true, "MaxWallDuration 7天"},
		{"MaxWallDuration", "2-06", true, "MaxWallDuration D-HH"},
		{"MaxWallDuration", "2-06:30", true, "MaxWallDuration D-HH:MM"},
		{"MaxWallDuration", "UNLIMITED", true, "MaxWallDuration UNLIMITED"},
		{"MaxWallDuration", "unlimited", true, "MaxWallDuration unlimited"},
		{"MaxWallDuration", "-1", true, "MaxWallDuration -1"},
		{"MaxWallDuration", "00:60:00", false, "MaxWallDuration 秒数 60 (非法)"},
		{"MaxWallDuration", "00:00:60", false, "MaxWallDuration 秒数 60 (非法)"},
		{"MaxWallDuration", "01:60", false, "MaxWallDuration 分钟 60 (非法)"},
		{"MaxWallDuration", "-02:00:00", false, "MaxWallDuration 负时段"},
		{"MaxWallDuration", "02:00:00;rm", false, "MaxWallDuration 分号注入"},
		{"MaxWallDuration", "02:00:00' || id", false, "MaxWallDuration 单引号注入"},
		{"MaxWallDuration", "02:00:00`reboot`", false, "MaxWallDuration 反引号注入"},
		{"MaxWallDuration", "forever", false, "MaxWallDuration 随意字符串"},
		{"MaxWallDuration", "2 days", false, "MaxWallDuration 包含空格"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_%s", tc.field, tc.desc), func(t *testing.T) {
			u := QOSUpdates{}
			switch tc.field {
			case "Description":
				u.Description = tc.payload
			case "Priority":
				u.Priority = tc.payload
			case "GrpTRES":
				u.GrpTRES = tc.payload
			case "MaxTRESPerUser":
				u.MaxTRESPerUser = tc.payload
			case "MaxJobsPerUser":
				u.MaxJobsPerUser = tc.payload
			case "MaxSubmitJobsPerUser":
				u.MaxSubmitJobsPerUser = tc.payload
			case "MaxWallDuration":
				u.MaxWallDuration = tc.payload
			}

			err := ValidateQOSFields(u)
			if tc.valid && err != nil {
				t.Errorf("expected valid for %s=%q, got error: %v", tc.field, tc.payload, err)
			}
			if !tc.valid && err == nil {
				t.Errorf("expected rejection for %s=%q, got nil error", tc.field, tc.payload)
			}

			// 测试 CreateQOS 中的行为
			if !tc.valid {
				if _, err := s.CreateQOS(context.Background(), "admin", "valid-qos", u, "req-adv"); err == nil {
					t.Errorf("CreateQOS expected rejection for %s=%q, got nil", tc.field, tc.payload)
				}
			}
		})
	}
}

// TestAdversarial_BusinessLogicAndProtections 测试业务保护与状态流转
func TestAdversarial_BusinessLogicAndProtections(t *testing.T) {
	// 1. 删除保护：禁止删除 normal QOS（不区分大小写）
	t.Run("ProtectNormalQOSDeletion", func(t *testing.T) {
		s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			t.Fatalf("runner MUST NOT be called for normal deletion: %v", args)
			return nil, nil
		})

		for _, name := range []string{"normal", "NORMAL", "Normal", "nOrMaL"} {
			err := s.DeleteQOS(context.Background(), "admin", name, "req-del-normal")
			if err == nil || !strings.Contains(err.Error(), "cannot delete default normal qos") {
				t.Errorf("DeleteQOS(%q) want 'cannot delete default normal qos', got %v", name, err)
			}
		}
	})

	// 2. 删除不存在的 QOS (sacctmgr 输出 NOTHING DELETED 或 Unknown QOS)
	t.Run("DeleteNonExistentQOS", func(t *testing.T) {
		s1, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			return []byte(" Nothing deleted\n"), nil
		})

		err1 := s1.DeleteQOS(context.Background(), "admin", "ghost-qos", "req-del-ghost")
		if !errors.Is(err1, ErrQOSNotFound) {
			t.Errorf("DeleteQOS with nothing deleted want ErrQOSNotFound, got %v", err1)
		}

		s2, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			return []byte("sacctmgr: error: Unknown QOS: ghost-qos\n"), nil
		})

		err2 := s2.DeleteQOS(context.Background(), "admin", "ghost-qos", "req-del-ghost")
		if !errors.Is(err2, ErrQOSNotFound) {
			t.Errorf("DeleteQOS with unknown qos want ErrQOSNotFound, got %v", err2)
		}
	})

	// 3. 修改不存在的 QOS (sacctmgr 输出 UNKNOWN QOS 或 NOTHING MODIFIED)
	t.Run("UpdateNonExistentQOS", func(t *testing.T) {
		s1, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			return []byte("sacctmgr: error: Unknown QOS: ghost-qos\n"), nil
		})
		err1 := s1.UpdateQOS(context.Background(), "admin", "ghost-qos", QOSUpdates{Priority: "100"}, "req-up-ghost")
		if !errors.Is(err1, ErrQOSNotFound) {
			t.Errorf("UpdateQOS with unknown qos want ErrQOSNotFound, got %v", err1)
		}

		s2, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			return []byte(" Nothing modified\n"), nil
		})
		err2 := s2.UpdateQOS(context.Background(), "admin", "ghost-qos", QOSUpdates{Priority: "100"}, "req-up-ghost")
		if !errors.Is(err2, ErrQOSNotFound) {
			t.Errorf("UpdateQOS with nothing modified want ErrQOSNotFound, got %v", err2)
		}
	})

	// 4. 空修改更新载荷拒绝
	t.Run("RejectEmptyUpdatePayload", func(t *testing.T) {
		s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			t.Fatalf("runner MUST NOT be called for empty updates: %v", args)
			return nil, nil
		})

		emptyUpdates := []QOSUpdates{
			{},
			{Description: "   ", Priority: "\t", GrpTRES: ""},
		}

		for _, u := range emptyUpdates {
			err := s.UpdateQOS(context.Background(), "admin", "any-qos", u, "req-up-empty")
			if err == nil || !strings.Contains(err.Error(), "no qos fields to update") {
				t.Errorf("UpdateQOS with empty updates want 'no qos fields to update', got %v", err)
			}
		}
	})

	// 5. Slurm 报告执行错误时正确包装返回
	t.Run("SlurmClusterExecutionError", func(t *testing.T) {
		s, _ := newPartitionService(t, func(args ...string) ([]byte, error) {
			return []byte("sacctmgr: error: Problem adding QOS: Duplicate entry\n"), nil
		})

		_, err := s.CreateQOS(context.Background(), "admin", "dup-qos", QOSUpdates{Priority: "100"}, "req-dup")
		if err == nil || !strings.Contains(err.Error(), "Duplicate entry") {
			t.Errorf("expected Duplicate entry error, got %v", err)
		}
	})
}

// TestAdversarial_ParseQOSListChaosFuzzing 测试解析器应对各种畸变输入的健壮性
func TestAdversarial_ParseQOSListChaosFuzzing(t *testing.T) {
	chaosInputs := []struct {
		name  string
		input string
	}{
		{"纯空字符串", ""},
		{"仅换行与空格", "\n\n   \t\n  \r\n\n"},
		{"仅无用分隔符", "|||||||||\n|||||\n|\n"},
		{"无竖线普通文本", "This is an error output\nSlurmDBD daemon is unreachable\n"},
		{"首行表头但后续全损坏", "Name|Priority|GrpTRES\ncorrupted line without pipes\n|||\n"},
		{"超多列超常数据", "qos1|100|" + strings.Repeat("gres/gpu=1|", 50) + "\n"},
		{"包含 Null 字节与非打印字符", "qos_null\x00|100|cpu=1\x01\x02|none\n"},
		{"Slurm 警告和错误混杂", "sacctmgr: warning: database latency high\nqos_valid|200|cpu=4|gres/gpu=1|01:00:00|2|5|Standard QOS\nsacctmgr: error: connection reset\n"},
		{"只有部分列数据", "partial_qos|500\n"},
	}

	for _, tc := range chaosInputs {
		t.Run(tc.name, func(t *testing.T) {
			// 确保绝不 panic
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseQOSList panicked on input %q: %v", tc.name, r)
				}
			}()

			res := ParseQOSList(tc.input)
			if res == nil {
				t.Fatalf("ParseQOSList returned nil slice on %q, want non-nil empty/populated slice", tc.name)
			}
		})
	}

	// 5000 行大规模解析压力测试
	t.Run("LargeScaleStressParsing", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("Name|Priority|GrpTRES|MaxTRESPerUser|MaxWallDuration|MaxJobsPerUser|MaxSubmitJobsPerUser|Description\n")
		for i := 0; i < 5000; i++ {
			sb.WriteString(fmt.Sprintf("qos_%d|%d|cpu=%d,mem=%dG|gres/gpu=1|02:00:00|%d|%d|Description for QOS %d\n",
				i, i*10, i%32+1, (i%32+1)*4, i%5+1, (i%5+1)*2, i))
		}

		res := ParseQOSList(sb.String())
		if len(res) != 5000 {
			t.Fatalf("ParseQOSList returned %d items, want 5000", len(res))
		}
		if res[0].Name != "qos_0" || res[4999].Name != "qos_4999" {
			t.Errorf("head/tail mismatch: head=%s tail=%s", res[0].Name, res[4999].Name)
		}
	})
}

// TestAdversarial_AuditLogVerification 验证 CRUD 操作后真实 SQLite 审计日志落库
func TestAdversarial_AuditLogVerification(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		return []byte(""), nil
	})

	ctx := context.Background()

	// 1. Create QOS
	createUpdates := QOSUpdates{
		Priority:       "500",
		GrpTRES:        "gres/gpu=4,cpu=32",
		MaxTRESPerUser: "gres/gpu=1,cpu=8",
		Description:    "AI Training QOS",
	}
	_, err := s.CreateQOS(ctx, "sec_admin", "ai-train", createUpdates, "req-audit-1")
	if err != nil {
		t.Fatalf("CreateQOS failed: %v", err)
	}

	// 2. Update QOS
	updateUpdates := QOSUpdates{
		MaxJobsPerUser: "2",
	}
	err = s.UpdateQOS(ctx, "sec_admin", "ai-train", updateUpdates, "req-audit-2")
	if err != nil {
		t.Fatalf("UpdateQOS failed: %v", err)
	}

	// 3. Delete QOS
	err = s.DeleteQOS(ctx, "sec_admin", "ai-train", "req-audit-3")
	if err != nil {
		t.Fatalf("DeleteQOS failed: %v", err)
	}

	// 检查 SQLite audit_log 表
	expectedActions := []string{"qos.create", "qos.modify", "qos.delete"}
	for _, action := range expectedActions {
		entries, err := st.ListAudit(ctx, "sec_admin", action, 10)
		if err != nil {
			t.Fatalf("ListAudit %s: %v", action, err)
		}
		found := false
		for _, e := range entries {
			if e.Target == "qos:ai-train" && e.Actor == "sec_admin" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected audit record for action %q on qos:ai-train was not found", action)
		}
	}
}
