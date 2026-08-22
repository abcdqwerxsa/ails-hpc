package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/store"
	_ "modernc.org/sqlite"
)

// =========================================================================================
// 1. Adversarial Shell Injection & Metacharacter Testing for User QOS
// =========================================================================================

func TestChallenger_UserQOS_AntiInjection_AllParameters(t *testing.T) {
	s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
		t.Fatalf("runner MUST NOT be called when validation fails: %v", args)
		return nil, nil
	})
	ctx := context.Background()

	// Seed valid tenant & user
	if _, err := st.CreateTenant(ctx, "valid-tenant", ""); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := st.CreateUser(ctx, store.NewUser{
		Username:   "validuser",
		Password:   "ValidPass12345",
		Role:       auth.RoleMember,
		TenantSlug: "valid-tenant",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	validReq := UserQOSUpdates{
		DefaultQOS: "normal",
		AllowedQOS: []string{"normal"},
	}

	// 1.1 Malicious Username Payloads
	maliciousUsernames := []struct {
		username string
		desc     string
	}{
		{"", "empty username"},
		{"user;rm -rf /", "semicolon command injection"},
		{"user' || id || '", "single quote escape"},
		{"user\" || id || \"", "double quote escape"},
		{"user`whoami`", "backtick subshell"},
		{"user$(whoami)", "dollar parentheses subshell"},
		{"user${IFS}id", "IFS environment variable expansion"},
		{"user|cat /etc/passwd", "pipe command injection"},
		{"user>out.txt", "output redirection"},
		{"user\nreboot", "newline character injection"},
		{"user\r\nreboot", "CRLF character injection"},
		{"user\x00inject", "null byte injection"},
		{"user bad", "contains whitespace"},
		{"user\tbad", "contains tab"},
		{"1user", "starts with digit"},
		{"-user", "starts with hyphen"},
		{"user🚀", "contains emoji"},
		{"用户", "non-ascii chinese characters"},
		{strings.Repeat("u", 33), "exceeds max length (33 chars)"},
	}

	for _, tc := range maliciousUsernames {
		t.Run("UsernameInjection_"+tc.desc, func(t *testing.T) {
			// SetUserQOS
			if err := s.SetUserQOS(ctx, "padmin", tc.username, "", validReq, "rid-inj"); err == nil {
				t.Errorf("SetUserQOS with %s (%q) must fail", tc.desc, tc.username)
			}
			// GetUserQOS
			if _, err := s.GetUserQOS(ctx, tc.username, ""); err == nil {
				t.Errorf("GetUserQOS with %s (%q) must fail", tc.desc, tc.username)
			}
		})
	}

	// 1.2 Malicious TenantSlug Payloads
	maliciousTenantSlugs := []struct {
		tenant string
		desc   string
	}{
		{"tenant;reboot", "semicolon command injection in tenantSlug"},
		{"tenant' || id", "single quote escape in tenantSlug"},
		{"tenant\" || id", "double quote escape in tenantSlug"},
		{"tenant$(whoami)", "dollar parentheses subshell in tenantSlug"},
		{"tenant|id", "pipe injection in tenantSlug"},
		{"tenant\nid", "newline in tenantSlug"},
		{"tenant\r\nreboot", "CRLF in tenantSlug"},
		{"tenant space", "space in tenantSlug"},
		{"1tenant", "starts with digit in tenantSlug"},
		{"-tenant", "starts with hyphen in tenantSlug"},
		{strings.Repeat("t", 33), "tenantSlug exceeds 32 chars"},
	}

	for _, tc := range maliciousTenantSlugs {
		t.Run("TenantSlugInjection_"+tc.desc, func(t *testing.T) {
			if err := s.SetUserQOS(ctx, "padmin", "validuser", tc.tenant, validReq, "rid-inj"); err == nil {
				t.Errorf("SetUserQOS with %s (%q) must fail", tc.desc, tc.tenant)
			}
			if _, err := s.GetUserQOS(ctx, "validuser", tc.tenant); err == nil {
				t.Errorf("GetUserQOS with %s (%q) must fail", tc.desc, tc.tenant)
			}
		})
	}

	// 1.3 Malicious DefaultQOS Payloads
	maliciousDefaultQOS := []struct {
		qos  string
		desc string
	}{
		{"qos;rm -rf /", "semicolon injection in defaultQOS"},
		{"qos' || id || '", "single quote injection in defaultQOS"},
		{"qos\" || id || \"", "double quote injection in defaultQOS"},
		{"qos`whoami`", "backtick injection in defaultQOS"},
		{"qos$(id)", "dollar parentheses injection in defaultQOS"},
		{"qos${IFS}run", "IFS injection in defaultQOS"},
		{"qos|id", "pipe injection in defaultQOS"},
		{"qos\nreboot", "newline in defaultQOS"},
		{"qos\r\nreboot", "CRLF in defaultQOS"},
		{"qos\x00inject", "null byte in defaultQOS"},
		{"1qos", "starts with digit in defaultQOS"},
		{"_qos", "starts with underscore in defaultQOS"},
		{"-qos", "starts with hyphen in defaultQOS"},
		{"qos name", "contains whitespace in defaultQOS"},
		{strings.Repeat("q", 33), "exceeds 32 chars in defaultQOS"},
	}

	for _, tc := range maliciousDefaultQOS {
		t.Run("DefaultQOSInjection_"+tc.desc, func(t *testing.T) {
			req := UserQOSUpdates{
				DefaultQOS: tc.qos,
				AllowedQOS: []string{tc.qos},
			}
			if err := ValidateUserQOSUpdates(&req); err == nil {
				t.Errorf("ValidateUserQOSUpdates with %s (%q) must fail", tc.desc, tc.qos)
			}
			if err := s.SetUserQOS(ctx, "padmin", "validuser", "", req, "rid-inj"); err == nil {
				t.Errorf("SetUserQOS with %s (%q) must fail", tc.desc, tc.qos)
			}
		})
	}

	// 1.4 Malicious AllowedQOS List Items
	maliciousAllowedQOS := []struct {
		allowed []string
		desc    string
	}{
		{[]string{"normal", "vip;rm -rf /"}, "semicolon in allowedQOS list item"},
		{[]string{"normal", "vip' || id"}, "single quote in allowedQOS list item"},
		{[]string{"normal", "vip$(whoami)"}, "subshell in allowedQOS list item"},
		{[]string{"normal", "vip|cat"}, "pipe in allowedQOS list item"},
		{[]string{"normal", "vip\nreboot"}, "newline in allowedQOS list item"},
		{[]string{"normal", "1vip"}, "digit prefix in allowedQOS list item"},
		{[]string{"normal", "_vip"}, "underscore prefix in allowedQOS list item"},
		{[]string{"normal", strings.Repeat("v", 33)}, "too long item in allowedQOS"},
	}

	for _, tc := range maliciousAllowedQOS {
		t.Run("AllowedQOSInjection_"+tc.desc, func(t *testing.T) {
			req := UserQOSUpdates{
				DefaultQOS: "normal",
				AllowedQOS: tc.allowed,
			}
			if err := ValidateUserQOSUpdates(&req); err == nil {
				t.Errorf("ValidateUserQOSUpdates with %s (%v) must fail", tc.desc, tc.allowed)
			}
			if err := s.SetUserQOS(ctx, "padmin", "validuser", "", req, "rid-inj"); err == nil {
				t.Errorf("SetUserQOS with %s (%v) must fail", tc.desc, tc.allowed)
			}
		})
	}
}

// =========================================================================================
// 2. Boundary Conditions, Inconsistency & Reset Keyword (-1) Testing
// =========================================================================================

func TestChallenger_UserQOS_BoundaryAndResetValidation(t *testing.T) {
	cases := []struct {
		name       string
		req        UserQOSUpdates
		wantErr    bool
		wantSubstr string
		validate   func(t *testing.T, req UserQOSUpdates)
	}{
		{
			name:       "DefaultQOS not in AllowedQOS",
			req:        UserQOSUpdates{DefaultQOS: "vip", AllowedQOS: []string{"normal", "high"}},
			wantErr:    true,
			wantSubstr: "must be included in allowedQos",
		},
		{
			name:       "Both DefaultQOS and AllowedQOS empty",
			req:        UserQOSUpdates{},
			wantErr:    true,
			wantSubstr: "at least one of defaultQos or allowedQos must be provided",
		},
		{
			name:       "AllowedQOS only contains spaces and empty items",
			req:        UserQOSUpdates{AllowedQOS: []string{"", "  ", "\t"}},
			wantErr:    true,
			wantSubstr: "at least one of defaultQos or allowedQos must be provided",
		},
		{
			name:       "Only DefaultQOS provided (valid case when not overriding allowedQos)",
			req:        UserQOSUpdates{DefaultQOS: "normal"},
			wantErr:    false,
			validate: func(t *testing.T, req UserQOSUpdates) {
				if req.DefaultQOS != "normal" || len(req.AllowedQOS) != 0 {
					t.Errorf("unexpected req: %+v", req)
				}
			},
		},
		{
			name:       "Only AllowedQOS provided (valid case when not overriding defaultQos)",
			req:        UserQOSUpdates{AllowedQOS: []string{"normal", "gpu-vip"}},
			wantErr:    false,
			validate: func(t *testing.T, req UserQOSUpdates) {
				if len(req.AllowedQOS) != 2 {
					t.Errorf("unexpected AllowedQOS length: %d", len(req.AllowedQOS))
				}
			},
		},
		{
			name: "Deduplication and Whitespace Trimming in AllowedQOS",
			req: UserQOSUpdates{
				DefaultQOS: "  gpu-vip  ",
				AllowedQOS: []string{" normal ", "gpu-vip", " normal ", " high ", "gpu-vip", "  "},
			},
			wantErr: false,
			validate: func(t *testing.T, req UserQOSUpdates) {
				if req.DefaultQOS != "gpu-vip" {
					t.Errorf("DefaultQOS not trimmed: %q", req.DefaultQOS)
				}
				if len(req.AllowedQOS) != 3 {
					t.Fatalf("AllowedQOS len want 3, got %d (%v)", len(req.AllowedQOS), req.AllowedQOS)
				}
				expected := []string{"normal", "gpu-vip", "high"}
				for i, v := range expected {
					if req.AllowedQOS[i] != v {
						t.Errorf("AllowedQOS[%d] = %q, want %q", i, req.AllowedQOS[i], v)
					}
				}
			},
		},
		{
			name: "Reset to default using -1 in DefaultQOS and AllowedQOS",
			req: UserQOSUpdates{
				DefaultQOS: "-1",
				AllowedQOS: []string{"-1"},
			},
			wantErr: false,
			validate: func(t *testing.T, req UserQOSUpdates) {
				if req.DefaultQOS != "-1" || len(req.AllowedQOS) != 1 || req.AllowedQOS[0] != "-1" {
					t.Errorf("unexpected reset req: %+v", req)
				}
			},
		},
		{
			name: "Reset DefaultQOS to -1 while keeping AllowedQOS containing -1",
			req: UserQOSUpdates{
				DefaultQOS: "-1",
				AllowedQOS: []string{"normal", "-1"},
			},
			wantErr: false,
		},
		{
			name: "Reset DefaultQOS to -1 with empty AllowedQOS",
			req: UserQOSUpdates{
				DefaultQOS: "-1",
			},
			wantErr: false,
		},
		{
			name: "Reset AllowedQOS to -1 with empty DefaultQOS",
			req: UserQOSUpdates{
				AllowedQOS: []string{"-1"},
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqCopy := tc.req
			err := ValidateUserQOSUpdates(&reqCopy)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.validate != nil {
					tc.validate(t, reqCopy)
				}
			}
		})
	}
}

// =========================================================================================
// 3. Complex `sacctmgr -nP show assoc` Output Parsing Matrix
// =========================================================================================

func TestChallenger_ParseUserQOS_ComplexOutputsMatrix(t *testing.T) {
	cases := []struct {
		name          string
		rawOut        string
		clusterUser   string
		parentAccount string
		wantUser      string
		wantAccount   string
		wantDefQOS    string
		wantAllowed   []string
	}{
		{
			name: "Standard pipe format without header",
			rawOut: "alice|hpc-lab|normal,gpu-vip,high|gpu-vip\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "gpu-vip",
			wantAllowed:   []string{"normal", "gpu-vip", "high"},
		},
		{
			name: "Standard pipe format with Header (User,Account,QOS,DefQOS)",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|hpc-lab|normal,gpu-vip|gpu-vip\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "gpu-vip",
			wantAllowed:   []string{"normal", "gpu-vip"},
		},
		{
			name: "Header column reordering with trailing pipe (DefQOS, QOS, Account, User|)",
			rawOut: "DefQOS|QOS|Account|User|\n" +
				"high|normal,high|hpc-lab|alice|\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "high",
			wantAllowed:   []string{"normal", "high"},
		},
		{
			name: "Header mixed case with extra whitespace and tabs",
			rawOut: "  USER  |  ACCOUNT  |  qos  |  DefQOS  \n" +
				"  alice  |  hpc-lab  |  normal,gpu-vip  |  gpu-vip  \n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "gpu-vip",
			wantAllowed:   []string{"normal", "gpu-vip"},
		},
		{
			name: "Delimiter variation: plus sign (+) separated QOS list",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|hpc-lab|normal+gpu-vip+ai-train|ai-train\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "ai-train",
			wantAllowed:   []string{"normal", "gpu-vip", "ai-train"},
		},
		{
			name: "Delimiter variation: spaces and tabs separated QOS list",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|hpc-lab|normal   gpu-vip \t high|gpu-vip\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "gpu-vip",
			wantAllowed:   []string{"normal", "gpu-vip", "high"},
		},
		{
			name: "Delimiter variation: mixed commas, pluses, spaces, duplicates",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|hpc-lab|normal, gpu-vip + high, normal ++ gpu-vip|high\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "high",
			wantAllowed:   []string{"normal", "gpu-vip", "high"},
		},
		{
			name: "Multi-row associations for same user across multiple accounts",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|root|normal|normal\n" +
				"alice|physics-lab|physics-qos|physics-qos\n" +
				"alice|hpc-lab|normal,gpu-vip,hpc-exclusive|gpu-vip\n" +
				"alice|bio-lab|bio-qos|bio-qos\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "gpu-vip",
			wantAllowed:   []string{"normal", "gpu-vip", "hpc-exclusive"},
		},
		{
			name: "Multi-row associations where target account is not matched -> fallback to first matching user row",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|first-lab|normal,first-qos|first-qos\n" +
				"alice|second-lab|second-qos|second-qos\n",
			clusterUser:   "alice",
			parentAccount: "non-existent-lab",
			wantUser:      "alice",
			wantAccount:   "first-lab",
			wantDefQOS:    "first-qos",
			wantAllowed:   []string{"normal", "first-qos"},
		},
		{
			name: "Missing DefQOS but QOS contains normal -> fallback DefQOS to normal",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|hpc-lab|gpu-vip,normal,high|\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "normal",
			wantAllowed:   []string{"gpu-vip", "normal", "high"},
		},
		{
			name: "Missing DefQOS and QOS does not contain normal -> fallback DefQOS to first allowed",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|hpc-lab|gpu-vip,ai-train|\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "gpu-vip",
			wantAllowed:   []string{"gpu-vip", "ai-train"},
		},
		{
			name: "Both QOS and DefQOS are empty -> fallback to [normal] and normal",
			rawOut: "User|Account|QOS|DefQOS\n" +
				"alice|hpc-lab||\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "normal",
			wantAllowed:   []string{"normal"},
		},
		{
			name: "Completely empty output from sacctmgr -> safe fallback to default [normal]",
			rawOut: "",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "normal",
			wantAllowed:   []string{"normal"},
		},
		{
			name: "Corrupted lines and noisy logs interspersed",
			rawOut: "sacctmgr: Warning: cluster sync running\n" +
				"||||\n" +
				"corrupted line\n" +
				"alice|hpc-lab|normal,gpu-vip|gpu-vip\n" +
				"another corrupted line\n",
			clusterUser:   "alice",
			parentAccount: "hpc-lab",
			wantUser:      "alice",
			wantAccount:   "hpc-lab",
			wantDefQOS:    "gpu-vip",
			wantAllowed:   []string{"normal", "gpu-vip"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := ParseUserQOS(tc.rawOut, tc.clusterUser, tc.parentAccount)
			if info == nil {
				t.Fatalf("ParseUserQOS returned nil")
			}
			if info.ClusterUser != tc.wantUser {
				t.Errorf("ClusterUser: got %q, want %q", info.ClusterUser, tc.wantUser)
			}
			if info.Account != tc.wantAccount {
				t.Errorf("Account: got %q, want %q", info.Account, tc.wantAccount)
			}
			if info.DefaultQOS != tc.wantDefQOS {
				t.Errorf("DefaultQOS: got %q, want %q", info.DefaultQOS, tc.wantDefQOS)
			}
			if len(info.AllowedQOS) != len(tc.wantAllowed) {
				t.Fatalf("AllowedQOS len: got %d (%v), want %d (%v)", len(info.AllowedQOS), info.AllowedQOS, len(tc.wantAllowed), tc.wantAllowed)
			}
			for i, v := range tc.wantAllowed {
				if info.AllowedQOS[i] != v {
					t.Errorf("AllowedQOS[%d] = %q, want %q", i, info.AllowedQOS[i], v)
				}
			}
		})
	}
}

// =========================================================================================
// 4. Command Generation, Syntax & Cache Flush Verification
// =========================================================================================

func TestChallenger_SetUserQOS_CommandSyntaxAndExecution(t *testing.T) {
	ctx := context.Background()

	testScenarios := []struct {
		name             string
		updates          UserQOSUpdates
		wantModifyTokens []string
		unwantTokens     []string
	}{
		{
			name: "Both AllowedQOS and DefaultQOS updated",
			updates: UserQOSUpdates{
				DefaultQOS: "gpu-vip",
				AllowedQOS: []string{"normal", "gpu-vip", "high"},
			},
			wantModifyTokens: []string{
				"sacctmgr -i modify user alice",
				"qos=normal,gpu-vip,high",
				"defaultqos=gpu-vip",
			},
		},
		{
			name: "Only AllowedQOS updated",
			updates: UserQOSUpdates{
				AllowedQOS: []string{"normal", "ai-train"},
			},
			wantModifyTokens: []string{
				"sacctmgr -i modify user alice",
				"qos=normal,ai-train",
			},
			unwantTokens: []string{"defaultqos="},
		},
		{
			name: "Only DefaultQOS updated",
			updates: UserQOSUpdates{
				DefaultQOS: "normal",
			},
			wantModifyTokens: []string{
				"sacctmgr -i modify user alice",
				"defaultqos=normal",
			},
			unwantTokens: []string{" qos="},
		},
		{
			name: "Reset both AllowedQOS and DefaultQOS using -1",
			updates: UserQOSUpdates{
				DefaultQOS: "-1",
				AllowedQOS: []string{"-1"},
			},
			wantModifyTokens: []string{
				"sacctmgr -i modify user alice",
				"qos=-1",
				"defaultqos=-1",
			},
		},
	}

	for _, tc := range testScenarios {
		t.Run(tc.name, func(t *testing.T) {
			var capturedCommands []string
			s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
				capturedCommands = append(capturedCommands, strings.Join(args, " "))
				return []byte(""), nil
			})

			_, _ = st.CreateTenant(ctx, "hpc-lab", "")
			_, _ = st.CreateUser(ctx, store.NewUser{
				Username:   "alice",
				Password:   "AlicePass123",
				Role:       auth.RoleMember,
				TenantSlug: "hpc-lab",
			})

			err := s.SetUserQOS(ctx, "padmin", "alice", "hpc-lab", tc.updates, "rid-cmd-test")
			if err != nil {
				t.Fatalf("SetUserQOS: %v", err)
			}

			if len(capturedCommands) < 2 {
				t.Fatalf("expected at least 2 commands (modify + scontrol reconfigure), got %d: %v", len(capturedCommands), capturedCommands)
			}

			modifyCmd := capturedCommands[0]
			for _, token := range tc.wantModifyTokens {
				if !strings.Contains(modifyCmd, token) {
					t.Errorf("modify command missing token %q: full = %q", token, modifyCmd)
				}
			}
			for _, unwant := range tc.unwantTokens {
				if strings.Contains(modifyCmd, unwant) {
					t.Errorf("modify command unexpectedly contains %q: full = %q", unwant, modifyCmd)
				}
			}

			// Ensure scontrol reconfigure was triggered immediately
			reconfigureCmd := capturedCommands[1]
			if !strings.Contains(reconfigureCmd, "scontrol reconfigure") {
				t.Errorf("second command must be scontrol reconfigure, got %q", reconfigureCmd)
			}
		})
	}
}

// =========================================================================================
// 5. Direct SQLite Audit Log Verification for User QOS
// =========================================================================================

func TestChallenger_UserQOS_AuditLog_DirectSQLiteVerification(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "user_qos_audit.db")
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

	_, _ = st.CreateTenant(ctx, "hpc-lab", "")
	_, _ = st.CreateUser(ctx, store.NewUser{
		Username:   "bob",
		Password:   "BobSecret123",
		Role:       auth.RoleMember,
		TenantSlug: "hpc-lab",
	})

	req := UserQOSUpdates{
		DefaultQOS: "gpu-vip",
		AllowedQOS: []string{"normal", "gpu-vip"},
	}

	err = s.SetUserQOS(ctx, "tenant_admin_bob", "bob", "hpc-lab", req, "rid-user-audit-123")
	if err != nil {
		t.Fatalf("SetUserQOS: %v", err)
	}

	// Direct SQLite check
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var actor, action, target, requestID, detail string
	err = db.QueryRowContext(ctx, "SELECT actor, action, target, request_id, detail FROM audit_log WHERE request_id='rid-user-audit-123'").
		Scan(&actor, &action, &target, &requestID, &detail)
	if err != nil {
		t.Fatalf("audit log query: %v", err)
	}

	if actor != "tenant_admin_bob" {
		t.Errorf("actor = %q, want tenant_admin_bob", actor)
	}
	if action != "qos.user.set" {
		t.Errorf("action = %q, want qos.user.set", action)
	}
	if target != "user:bob" {
		t.Errorf("target = %q, want user:bob", target)
	}

	var detailMap map[string]any
	if err := json.Unmarshal([]byte(detail), &detailMap); err != nil {
		t.Fatalf("detail is not valid JSON: %v", err)
	}
	if detailMap["tenant"] != "hpc-lab" {
		t.Errorf("detail tenant = %v, want hpc-lab", detailMap["tenant"])
	}
	if detailMap["defaultQos"] != "gpu-vip" && detailMap["defaultQOS"] != "gpu-vip" {
		t.Errorf("detail defaultQos = %v", detailMap)
	}
}

// =========================================================================================
// 6. GetAvailableQOS Full Object Mapping & Edge Cases
// =========================================================================================

func TestChallenger_GetAvailableQOS_ComprehensiveMapping(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name          string
		showAssocOut  string
		showQOSOut    string
		wantDefQOS    string
		wantLen       int
		assertAllowed func(t *testing.T, allowed []QOS)
	}{
		{
			name:         "Full QOS Objects matching from ListQOS",
			showAssocOut: "alice|hpc-lab|normal,gpu-vip|gpu-vip\n",
			showQOSOut: "Name|Priority|GrpTRES|MaxTRESPU|MaxWall|MaxJobsPU|MaxSubmitPU|Description\n" +
				"normal|0||||||Standard Normal QOS\n" +
				"gpu-vip|1000|gres/gpu=8|gres/gpu=2|02:00:00|2|5|VIP Dedicated QOS\n" +
				"other-qos|500||||||Other QOS\n",
			wantDefQOS: "gpu-vip",
			wantLen:    2,
			assertAllowed: func(t *testing.T, allowed []QOS) {
				if allowed[0].Name != "normal" || allowed[0].Priority != "0" {
					t.Errorf("allowed[0] mismatch: %+v", allowed[0])
				}
				if allowed[1].Name != "gpu-vip" || allowed[1].Priority != "1000" || allowed[1].GrpTRES != "gres/gpu=8" || allowed[1].MaxTRESPerUser != "gres/gpu=2" {
					t.Errorf("allowed[1] mismatch: %+v", allowed[1])
				}
			},
		},
		{
			name:         "QOS in association but missing in Slurm QOS definitions -> creates stub QOS",
			showAssocOut: "alice|hpc-lab|normal,deleted-qos|normal\n",
			showQOSOut: "Name|Priority|GrpTRES|MaxTRESPU|MaxWall|MaxJobsPU|MaxSubmitPU|Description\n" +
				"normal|0||||||Standard Normal QOS\n",
			wantDefQOS: "normal",
			wantLen:    2,
			assertAllowed: func(t *testing.T, allowed []QOS) {
				if allowed[0].Name != "normal" {
					t.Errorf("allowed[0] = %q", allowed[0].Name)
				}
				if allowed[1].Name != "deleted-qos" || allowed[1].Priority != "" {
					t.Errorf("allowed[1] stub mismatch: %+v", allowed[1])
				}
			},
		},
		{
			name:         "Empty association -> fallback to default normal",
			showAssocOut: "alice|hpc-lab||\n",
			showQOSOut: "Name|Priority|GrpTRES|MaxTRESPU|MaxWall|MaxJobsPU|MaxSubmitPU|Description\n" +
				"normal|0||||||Standard Normal QOS\n",
			wantDefQOS: "normal",
			wantLen:    1,
			assertAllowed: func(t *testing.T, allowed []QOS) {
				if allowed[0].Name != "normal" {
					t.Errorf("allowed[0] = %q", allowed[0].Name)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, st := newPartitionService(t, func(args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				if strings.Contains(cmd, "show assoc") {
					return []byte(tc.showAssocOut), nil
				}
				if strings.Contains(cmd, "show qos") {
					return []byte(tc.showQOSOut), nil
				}
				return []byte(""), nil
			})

			_, _ = st.CreateTenant(ctx, "hpc-lab", "")
			_, _ = st.CreateUser(ctx, store.NewUser{
				Username:   "alice",
				Password:   "AlicePass123",
				Role:       auth.RoleMember,
				TenantSlug: "hpc-lab",
			})

			resp, err := s.GetAvailableQOS(ctx, "alice", "hpc-lab")
			if err != nil {
				t.Fatalf("GetAvailableQOS: %v", err)
			}
			if resp.DefaultQOS != tc.wantDefQOS {
				t.Errorf("DefaultQOS: got %q, want %q", resp.DefaultQOS, tc.wantDefQOS)
			}
			if len(resp.AllowedQOS) != tc.wantLen {
				t.Fatalf("AllowedQOS len: got %d, want %d", len(resp.AllowedQOS), tc.wantLen)
			}
			if tc.assertAllowed != nil {
				tc.assertAllowed(t, resp.AllowedQOS)
			}
		})
	}
}
