package jobs_test

// 安全审计 2026-08-19 P0-1 回归：作业名/分区白名单。Name 会拼进容器内 sh -c 的
// 脚本路径——`;`/`$()`/反引号即 root 命令注入、`/`/`..` 即任意路径写。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ails-hpc/pkg/services/jobs"
)

func TestSubmitJob_NameWhitelist(t *testing.T) {
	cases := []struct {
		name     string
		part     string
		wantCode int
	}{
		// 注入载荷（P0-1 审计中的真实攻击面）
		{"ok-name", "standard", http.StatusOK},
		{"x; touch /tmp/pwned", "", http.StatusBadRequest},
		{"x$(id)", "", http.StatusBadRequest},
		{"x`id`", "", http.StatusBadRequest},
		{"../../etc/cron.d/pwn", "", http.StatusBadRequest},
		{"a/b", "", http.StatusBadRequest},
		{"'quote", "", http.StatusBadRequest},
		{"", "stand;ard", http.StatusBadRequest},
		{"ok-name2", "performance", http.StatusOK},
	}
	for _, c := range cases {
		mockServer, router := setupTestRouter()
		body, _ := json.Marshal(jobs.SubmitJobRequest{
			Name: c.name, Partition: c.part, TimeLimit: "60",
			Script: "#!/bin/bash\necho hi",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/v1/slurm/jobs/submit", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != c.wantCode {
			t.Errorf("name=%q part=%q: want %d got %d body=%s", c.name, c.part, c.wantCode, w.Code, w.Body.String())
		}
		mockServer.Close()
	}
}
