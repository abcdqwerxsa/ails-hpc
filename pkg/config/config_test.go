package config_test

import (
	"testing"
	"time"

	"ails-hpc/pkg/config"
)

func TestLoad_RequiresJWTSecret(t *testing.T) {
	t.Setenv("AILS_JWT_SECRET", "")
	if _, err := config.Load(); err == nil {
		t.Fatalf("expected error when AILS_JWT_SECRET is empty (fail-closed)")
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("AILS_JWT_SECRET", "test-secret-32-bytes-long-aaaa-bbbb")
	// 其余 env 显式置空以测默认值
	t.Setenv("AILS_PORT", "")
	t.Setenv("SLURMRESTD_URL", "")
	t.Setenv("AILS_SLURM_USER", "")
	t.Setenv("AILS_USERS_FILE", "")
	t.Setenv("AILS_DEPLOY_HOST", "")
	t.Setenv("AILS_TOKEN_TTL", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenPort != "8090" {
		t.Errorf("ListenPort default = %q, want 8090", cfg.ListenPort)
	}
	if cfg.SlurmRESTDURL != "http://192.168.20.226:6820" {
		t.Errorf("SlurmRESTDURL default = %q", cfg.SlurmRESTDURL)
	}
	if cfg.SlurmUserName != "hpcuser" {
		t.Errorf("SlurmUserName default = %q", cfg.SlurmUserName)
	}
	if cfg.UsersFile != "config/users.yaml" {
		t.Errorf("UsersFile default = %q", cfg.UsersFile)
	}
	if cfg.DeployHost != "192.168.20.226" {
		t.Errorf("DeployHost default = %q", cfg.DeployHost)
	}
	if cfg.TokenTTL != 24*time.Hour {
		t.Errorf("TokenTTL default = %v, want 24h", cfg.TokenTTL)
	}
	if string(cfg.JWTSecret) != "test-secret-32-bytes-long-aaaa-bbbb" {
		t.Errorf("JWTSecret not populated from env")
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("AILS_JWT_SECRET", "sekret")
	t.Setenv("AILS_PORT", "9999")
	t.Setenv("SLURMRESTD_URL", "http://slurm:6820")
	t.Setenv("AILS_DEPLOY_HOST", "10.0.0.5")
	t.Setenv("AILS_TOKEN_TTL", "2h")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenPort != "9999" || cfg.SlurmRESTDURL != "http://slurm:6820" ||
		cfg.DeployHost != "10.0.0.5" || cfg.TokenTTL != 2*time.Hour {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
}
