package auth

import (
	"strings"
	"sync"
	"time"
)

// 登录防爆破（roadmap 2.1）：按用户名+来源 IP 计数的滑动失败窗口。
//
// 策略：同一 (username, ip) 在 window 内失败 lockoutAfter 次 → 锁定 lockout
// 时长（窗口内再试直接拒绝，不比对密码——防在线穷举与 timing 侧信道）。
// 成功登录清零该键计数。纯内存态（apiserver 单实例；重启即清，可接受——
// 攻击者视角重启窗口不构成有效绕过路径，且失败仍会写审计日志）。
const (
	rateWindow        = 5 * time.Minute
	rateLockout       = 10 * time.Minute
	lockoutAfterFails = 5
)

type rateEntry struct {
	fails    int
	firstAt  time.Time
	lockedTo time.Time // 非零=锁定中
}

// RateLimiter 登录失败限速器（并发安全）。
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateEntry
}

// NewRateLimiter 构造限速器。
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{entries: make(map[string]*rateEntry)}
}

// Allow 检查该 (username, ip) 当前是否允许尝试登录。
// 锁定中返回 false（剩余锁定时长）。
func (r *RateLimiter) Allow(username, ip string) (bool, time.Duration) {
	if r == nil {
		return true, 0 // 未启用（零值/nil 容错）
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[key(username, ip)]
	if !ok {
		return true, 0
	}
	if e.lockedTo.After(time.Now()) {
		return false, time.Until(e.lockedTo)
	}
	return true, 0
}

// RecordFailure 记录一次失败；达到阈值进入锁定。返回是否触发锁定。
func (r *RateLimiter) RecordFailure(username, ip string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key(username, ip)
	e, ok := r.entries[k]
	now := time.Now()
	if !ok || now.Sub(e.firstAt) > rateWindow {
		// 新窗口（首失败或旧窗口已滚动）
		r.entries[k] = &rateEntry{fails: 1, firstAt: now}
		return false
	}
	e.fails++
	if e.fails >= lockoutAfterFails {
		e.lockedTo = now.Add(rateLockout)
		return true
	}
	return false
}

// RecordSuccess 成功登录：清零该键（避免历史失败误伤合法用户）。
func (r *RateLimiter) RecordSuccess(username, ip string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, key(username, ip))
}

func key(username, ip string) string {
	return strings.ToLower(strings.TrimSpace(username)) + "|" + strings.TrimSpace(ip)
}
