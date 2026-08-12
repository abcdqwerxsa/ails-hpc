// Package httpx renders the canonical API error envelope for the AILS HPC
// monolith. Every HTTP error path funnels through render, so the response shape
// is defined in exactly one place.
//
// Canonical body: {"error": <msg>, "request_id"?: <rid>, ...extras}
//   - error       always present (the web UI reads data.error)
//   - request_id  echoed when the requestIDMiddleware injected one
//   - code        never present (HTTP status is authoritative; jobs used to
//                 emit a redundant code field — removed)
//
// 4xx errors surface their message verbatim (it is about the client's request);
// 5xx errors go through Internal, which logs the real error server-side and
// substitutes a generic message so internal detail never leaks.
package httpx

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Extra carries optional domain-specific fields merged into an error body —
// e.g. {"required": [...]} on a 403, {"status": "DOWN"} on a cluster-down 503,
// or {"status": "STARTING"} for an IDE session still starting. Canonical keys
// ("error", "request_id") are applied last and always win, so an Extra can
// never clobber the contract fields.
type Extra map[string]any

// render is the single render path for every error response. It merges extras,
// then overlays the canonical fields, then writes the status + body and aborts
// the remaining handler chain. Aborting in a leaf handler is a harmless no-op
// (every call site returns immediately) and is required in middleware.
func render(c *gin.Context, status int, msg string, extras []Extra) {
	body := gin.H{}
	for _, e := range extras {
		for k, v := range e {
			body[k] = v
		}
	}
	body["error"] = msg
	if rid := c.GetString("request_id"); rid != "" {
		body["request_id"] = rid
	}
	c.AbortWithStatusJSON(status, body)
}

// Error renders a client-facing error envelope and aborts. Client errors are
// not logged — they are expected traffic. Use the status-specific wrappers
// (BadRequest, Unauthorized, ...) where one fits; reach for Error directly only
// for uncommon status codes.
func Error(c *gin.Context, status int, msg string, extras ...Extra) {
	render(c, status, msg, extras)
}

// Internal handles a server error (5xx). It logs the real error server-side
// (with the operation and request id for correlation against access logs) and
// returns a generic message to the client so internal detail — slurmrestd
// connection strings, file paths, etc. — is never leaked. Extras are still
// merged, so semantic fields like {"status":"DOWN"} survive on 5xx bodies.
func Internal(c *gin.Context, op string, err error, extras ...Extra) {
	rid := c.GetString("request_id")
	errMsg := "<nil>"
	if err != nil {
		errMsg = err.Error()
	}
	slog.Error("http_error", "op", op, "err", errMsg, "rid", rid, "status", http.StatusInternalServerError)
	render(c, http.StatusInternalServerError, "internal server error", extras)
}

// BadRequest responds 400.
func BadRequest(c *gin.Context, msg string, extras ...Extra) {
	render(c, http.StatusBadRequest, msg, extras)
}

// Unauthorized responds 401.
func Unauthorized(c *gin.Context, msg string, extras ...Extra) {
	render(c, http.StatusUnauthorized, msg, extras)
}

// Forbidden responds 403 and carries the roles that would have been permitted
// under the "required" key, alongside any caller-supplied extras.
func Forbidden(c *gin.Context, msg string, requiredRoles []string, extras ...Extra) {
	merged := make([]Extra, 0, 1+len(extras))
	merged = append(merged, Extra{"required": requiredRoles})
	merged = append(merged, extras...)
	render(c, http.StatusForbidden, msg, merged)
}

// NotFound responds 404.
func NotFound(c *gin.Context, msg string, extras ...Extra) {
	render(c, http.StatusNotFound, msg, extras)
}

// ServiceUnavailable responds 503.
func ServiceUnavailable(c *gin.Context, msg string, extras ...Extra) {
	render(c, http.StatusServiceUnavailable, msg, extras)
}
