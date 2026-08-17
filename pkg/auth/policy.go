package auth

// 密码与会话策略（A1）。策略为代码常量——所有密码写入点（自助改密/管理员重置/
// 建户初始密码）统一走 ValidatePasswordPolicy。

import (
	"context"
	"errors"
	"unicode"
)

// PolicyStore 是密码策略写面（生产 = sqlite store；内存/yaml 库不实现 →
// handler 降级为仅复杂度校验，历史/强制标记不可用）。
type PolicyStore interface {
	// CheckPasswordHistory 新密码与最近 N 次重复时返回 ErrPasswordReused 语义错误。
	CheckPasswordHistory(ctx context.Context, username, newPassword string) error
	// SetPasswordWithHistory 自助改密落库（清 must_change + 旧哈希入历史 + bump 版本）。
	SetPasswordWithHistory(ctx context.Context, username, newHash string) error
}

// PasswordPolicyMinLen 最小长度。
const PasswordPolicyMinLen = 8

// ErrWeakPasswordPolicy 不满足复杂度策略（大小写/数字/符号 + 最小长度）。
var ErrWeakPasswordPolicy = errors.New("password must be at least 8 characters and contain upper-case, lower-case, digit and symbol characters")

// ValidatePasswordPolicy 校验密码复杂度：≥8 字符且包含大写、小写、数字、符号各至少一个。
// 返回 nil=通过；ErrWeakPasswordPolicy=不达标。
func ValidatePasswordPolicy(pw string) error {
	if len(pw) < PasswordPolicyMinLen {
		return ErrWeakPasswordPolicy
	}
	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSymbol = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit || !hasSymbol {
		return ErrWeakPasswordPolicy
	}
	return nil
}
