package slurmrest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// softFailServer 模拟 slurmrestd v0.0.37 的关键行为：对无效令牌返回
// 200 + errors[]（error_code 5005）而非 401。只有持有 validToken 的请求得到正常响应。
type softFailServer struct {
	*httptest.Server
	valid     atomic.Value // string
	requests  atomic.Int32
	lastToken atomic.Value
}

func newSoftFailServer(validToken string) *softFailServer {
	s := &softFailServer{}
	s.valid.Store(validToken)
	mux := http.NewServeMux()
	mux.HandleFunc("/slurm/v0.0.37/ping", func(w http.ResponseWriter, r *http.Request) {
		s.serve(w, r, `{"errors":[],"pings":[{"ping":"UP"}]}`)
	})
	mux.HandleFunc("/slurm/v0.0.37/job/submit", func(w http.ResponseWriter, r *http.Request) {
		s.serve(w, r, `{"errors":[],"job_id":42,"step_id":"BATCH"}`)
	})
	s.Server = httptest.NewServer(mux)
	return s
}

func (s *softFailServer) serve(w http.ResponseWriter, r *http.Request, okBody string) {
	s.requests.Add(1)
	s.lastToken.Store(r.Header.Get("X-SLURM-USER-TOKEN"))
	if r.Header.Get("X-SLURM-USER-TOKEN") == s.valid.Load().(string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody))
		return
	}
	// 软失败：HTTP 200 + errors[]（slurmrestd v0.0.37 对无效令牌的真实行为）
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"errors":[{"error_code":5005,"error":"Zero Bytes were transmitted or received"}]}`))
}

func newTestClient(url, minted string) *Client {
	return &Client{
		BaseURL:   url,
		UserToken: "stale-token",
		HTTPClient: &http.Client{
			Timeout: 5e9,
		},
		tokens: map[string]string{"alice": "stale-token"}, // 预置陈旧 per-user 缓存
		mint:   func(string) string { return minted },
	}
}

// TestSoftFail_RootPathRefreshesToken：root 路径缓存令牌陈旧（slurmctld 重建后典型态）→
// 200+errors 软失败 → 自动刷新重试 → 成功。
func TestSoftFail_RootPathRefreshesToken(t *testing.T) {
	srv := newSoftFailServer("fresh-token")
	defer srv.Close()
	c := newTestClient(srv.URL, "fresh-token") // UserToken=stale, mint→fresh

	resp, err := c.Ping()
	if err != nil {
		t.Fatalf("Ping should recover via refresh-retry: %v", err)
	}
	if len(resp.Pings) == 0 || resp.Pings[0].Ping != "UP" {
		t.Fatalf("unexpected ping resp: %+v", resp)
	}
	if got := srv.requests.Load(); got != 2 {
		t.Errorf("want exactly 2 requests (stale + refreshed), got %d", got)
	}
}

// TestSoftFail_PerUserSubmitRefreshesToken：per-user 提交路径同样自愈。
func TestSoftFail_PerUserSubmitRefreshesToken(t *testing.T) {
	srv := newSoftFailServer("fresh-token")
	defer srv.Close()
	c := newTestClient(srv.URL, "fresh-token") // tokens[alice]=stale

	req := &SlurmJobSubmitReq{Script: "#!/bin/bash\necho hi\n"}
	resp, err := c.SubmitJobAs(req, "alice")
	if err != nil {
		t.Fatalf("SubmitJobAs should recover via refresh-retry: %v", err)
	}
	if resp.JobID != 42 {
		t.Errorf("job_id = %d, want 42", resp.JobID)
	}
	if last := srv.lastToken.Load().(string); last != "fresh-token" {
		t.Errorf("retry must use refreshed token, used %q", last)
	}
}

// TestSoftFail_PersistentErrorSurfaces：刷新后仍软失败 → 如实报错（不再静默空数据）。
func TestSoftFail_PersistentErrorSurfaces(t *testing.T) {
	srv := newSoftFailServer("never-matches")
	defer srv.Close()
	c := newTestClient(srv.URL, "another-wrong-token")

	if _, err := c.Ping(); err == nil || !strings.Contains(err.Error(), "slurmrestd errors") {
		t.Fatalf("persistent soft-fail must surface an error, got %v", err)
	}
}

// TestSoftErrorsProbe：探测助手对各类响应体的判定。
func TestSoftErrorsProbe(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"normal empty errors", `{"errors":[]}`, 0},
		{"soft fail", `{"errors":[{"error_code":5005,"error":"Zero Bytes"}]}`, 1},
		{"not json", `plain text`, 0},
		{"missing field", `{"job_id":1}`, 0},
	}
	for _, tc := range cases {
		if got := len(softErrors([]byte(tc.body))); got != tc.want {
			t.Errorf("%s: softErrors = %d msgs, want %d", tc.name, got, tc.want)
		}
	}
	_ = fmt.Sprint() // keep fmt import if cases change
}
