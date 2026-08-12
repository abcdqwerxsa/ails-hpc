package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// doRequest mounts a one-route engine that runs fn in the handler, optionally
// preceded by a stub middleware injecting a request_id, then serves a single
// request and returns the recorder plus the decoded JSON body.
func doRequest(t *testing.T, rid string, fn func(c *gin.Context)) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if rid != "" {
		r.Use(func(c *gin.Context) { c.Set("request_id", rid); c.Next() })
	}
	r.GET("/", func(c *gin.Context) { fn(c) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func TestErrorEnvelopeShape(t *testing.T) {
	w, body := doRequest(t, "rid-1", func(c *gin.Context) {
		Error(c, http.StatusBadRequest, "bad", Extra{"foo": 1})
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if body["error"] != "bad" {
		t.Errorf("error = %v, want \"bad\"", body["error"])
	}
	if body["foo"] != float64(1) { // JSON numbers decode as float64
		t.Errorf("foo = %v, want 1", body["foo"])
	}
	if body["request_id"] != "rid-1" {
		t.Errorf("request_id = %v, want rid-1", body["request_id"])
	}
}

func TestRequestIDPropagated(t *testing.T) {
	_, body := doRequest(t, "abc-123", func(c *gin.Context) {
		BadRequest(c, "nope")
	})
	if body["request_id"] != "abc-123" {
		t.Errorf("request_id = %v, want abc-123", body["request_id"])
	}
}

func TestRequestIDOmittedWhenEmpty(t *testing.T) {
	_, body := doRequest(t, "", func(c *gin.Context) {
		BadRequest(c, "nope")
	})
	if _, ok := body["request_id"]; ok {
		t.Errorf("request_id key present in body, want it omitted: %v", body)
	}
}

func TestCanonicalFieldsWinOverExtras(t *testing.T) {
	_, body := doRequest(t, "rid-x", func(c *gin.Context) {
		Error(c, http.StatusBadRequest, "real", Extra{"error": "fake", "request_id": "fake"})
	})
	if body["error"] != "real" {
		t.Errorf("error = %v, canonical field clobbered by extra", body["error"])
	}
	if body["request_id"] != "rid-x" {
		t.Errorf("request_id = %v, canonical field clobbered by extra", body["request_id"])
	}
}

func TestInternalDoesNotLeak(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	w, body := doRequest(t, "rid-leak", func(c *gin.Context) {
		Internal(c, "SubmitJob", errors.New("DB password is hunter2"))
	})

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if body["error"] != "internal server error" {
		t.Errorf("error = %v, want generic \"internal server error\"", body["error"])
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Errorf("response body leaked internal detail: %s", w.Body.String())
	}

	logOut := buf.String()
	if !strings.Contains(logOut, "hunter2") {
		t.Errorf("server log missing real error; log = %q", logOut)
	}
	if !strings.Contains(logOut, "op=SubmitJob") {
		t.Errorf("server log missing op; log = %q", logOut)
	}
	if !strings.Contains(logOut, "rid=rid-leak") {
		t.Errorf("server log missing rid; log = %q", logOut)
	}
}

func TestInternalPreservesExtras(t *testing.T) {
	w, body := doRequest(t, "rid-extra", func(c *gin.Context) {
		Internal(c, "GetStatus", errors.New("slurmrestd unreachable"), Extra{"status": "DOWN"})
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if body["error"] != "internal server error" {
		t.Errorf("error = %v, want generic", body["error"])
	}
	if body["status"] != "DOWN" {
		t.Errorf("status extra = %v, want DOWN", body["status"])
	}
}

func TestWrappers(t *testing.T) {
	cases := []struct {
		name     string
		fn       func(c *gin.Context)
		wantCode int
	}{
		{"bad request", func(c *gin.Context) { BadRequest(c, "bad") }, 400},
		{"unauthorized", func(c *gin.Context) { Unauthorized(c, "no") }, 401},
		{"not found", func(c *gin.Context) { NotFound(c, "gone") }, 404},
		{"service unavailable", func(c *gin.Context) { ServiceUnavailable(c, "down") }, 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := doRequest(t, "", tc.fn)
			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tc.wantCode)
			}
		})
	}

	// Forbidden additionally carries the permitted roles under "required".
	_, body := doRequest(t, "", func(c *gin.Context) {
		Forbidden(c, "forbidden: role 'member' is not permitted", []string{"admin"})
	})
	if body["error"] == nil {
		t.Errorf("Forbidden missing error field")
	}
	required, ok := body["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "admin" {
		t.Errorf("required = %v, want [\"admin\"]", body["required"])
	}
}
