package auth_test

import (
	"testing"

	"ails-hpc/pkg/auth"
)

// TestRateLimiter_LockoutAfterFails：5 次失败进锁定，锁定期 Allow=false；成功清零。
func TestRateLimiter_LockoutAfterFails(t *testing.T) {
	r := auth.NewRateLimiter()
	for i := 1; i <= 4; i++ {
		if ok, _ := r.Allow("u", "1.2.3.4"); !ok {
			t.Fatalf("attempt %d should be allowed (below threshold)", i)
		}
		r.RecordFailure("u", "1.2.3.4")
	}
	// 第 5 次失败触发锁定
	if !r.RecordFailure("u", "1.2.3.4") {
		t.Fatal("5th failure should trigger lockout")
	}
	if ok, _ := r.Allow("u", "1.2.3.4"); ok {
		t.Error("locked key must be denied")
	}
	// 其他 IP 不受影响（按 用户+IP 键控）
	if ok, _ := r.Allow("u", "5.6.7.8"); !ok {
		t.Error("different ip must be unaffected")
	}
	// 其他用户不受影响
	if ok, _ := r.Allow("other", "1.2.3.4"); !ok {
		t.Error("different user must be unaffected")
	}
	// 成功登录清零
	r.RecordSuccess("u", "1.2.3.4")
	if ok, _ := r.Allow("u", "1.2.3.4"); !ok {
		t.Error("success must reset the counter")
	}
}
