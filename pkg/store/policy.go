package store

// A1 密码与会话策略的 store 写面：密码历史（N 次不可重用）、强制改密标记、
// 会话台账与全设备登出。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"ails-hpc/pkg/auth"
)

// passwordHistoryKeep 每用户保留的历史哈希条数（新密码不可与最近 N 次重复）。
const passwordHistoryKeep = 5

// ErrPasswordReused 新密码与最近 N 次历史重复（A1）。
var ErrPasswordReused = errors.New("store: password was used recently and cannot be reused")

// CheckPasswordHistory 判断新密码是否与最近 N 次历史重复（bcrypt 逐条比对）。
// 无历史/库不支持 → 通过。
func (s *sqliteStore) CheckPasswordHistory(ctx context.Context, username, newPassword string) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT password_hash FROM password_history
		WHERE username = ? ORDER BY id DESC LIMIT ?`, username, passwordHistoryKeep)
	if err != nil {
		return nil // 历史读取失败不阻断改密（策略尽力而为）
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			continue
		}
		if auth.CompareHashAndPassword(hash, newPassword) == nil {
			return fmt.Errorf("%w (last %d)", ErrPasswordReused, passwordHistoryKeep)
		}
	}
	return nil
}

// appendPasswordHistory 记录旧哈希进历史并裁剪到保留窗口。
func (s *sqliteStore) appendPasswordHistory(ctx context.Context, username, hash string) {
	if hash == "" {
		return
	}
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO password_history (username, password_hash) VALUES (?, ?)`, username, hash)
	_, _ = s.db.ExecContext(ctx, `
		DELETE FROM password_history WHERE username = ? AND id NOT IN (
			SELECT id FROM password_history WHERE username = ? ORDER BY id DESC LIMIT ?
		)`, username, username, passwordHistoryKeep)
}

// SetPasswordWithHistory 自助改密落库：历史校验由 handler 先行调用
// CheckPasswordHistory；这里旧哈希入历史 + 清 must_change_password + bump 版本。
func (s *sqliteStore) SetPasswordWithHistory(ctx context.Context, username, newHash string) error {
	var oldHash string
	var rows int
	err := s.db.QueryRowContext(ctx,
		`SELECT password_hash, 1 FROM users WHERE username = ?`, username).Scan(&oldHash, &rows)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, must_change_password = 0,
			token_version = token_version + 1, updated_at = datetime('now')
		WHERE username = ?`, newHash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return auth.ErrInvalidCredentials
	}
	s.appendPasswordHistory(ctx, username, oldHash)
	return nil
}

// ResetUserPassword 管理员重置（已有方法，A1 起加强）：must_change_password=1
// （被重置者下次登录强制改密）+ 旧哈希入历史。
func (s *sqliteStore) ResetUserPassword(ctx context.Context, username, newHash string) error {
	if !isBcryptHash(newHash) {
		return fmt.Errorf("%w: %q is not a bcrypt hash", ErrInvalidHash, newHash)
	}
	var oldHash string
	err := s.db.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE username = ?`, username).Scan(&oldHash)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, must_change_password = 1,
			token_version = token_version + 1, updated_at = datetime('now')
		WHERE username = ?`, newHash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	s.appendPasswordHistory(ctx, username, oldHash)
	return nil
}

func isBcryptHash(h string) bool {
	return len(h) > 3 && (h[:3] == "$2a" || h[:3] == "$2b" || h[:3] == "$2y" || h[:4] == "$2")
}

// RecordLogin 台账一条会话（登录成功时；token_version 记签发时版本——
// 版本 bump 后旧会话整体失效，无需逐条吊销）。
func (s *sqliteStore) RecordLogin(ctx context.Context, username, ip, userAgent string, expiresAt time.Time) {
	ver, ok := s.UserVersion(username)
	if !ok {
		return
	}
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO sessions (username, expires_at, ip, user_agent, token_version)
		VALUES (?, ?, ?, ?, ?)`,
		username, expiresAt.UTC().Format(time.RFC3339), ip, userAgent, ver)
}

// ListSessions 用户当前有效会话（未过期且 token_version 与当前一致——
// 改密/登出全部设备后即整批失效）。
func (s *sqliteStore) ListSessions(ctx context.Context, username string) ([]auth.SessionEntry, error) {
	ver, ok := s.UserVersion(username)
	if !ok {
		return nil, fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, issued_at, expires_at, ip, user_agent, token_version
		FROM sessions WHERE username = ? ORDER BY id DESC LIMIT 50`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	out := []auth.SessionEntry{}
	for rows.Next() {
		var si auth.SessionEntry
		var un string
		var sv int
		if err := rows.Scan(&si.ID, &un, &si.IssuedAt, &si.ExpiresAt, &si.IP, &si.UserAgent, &sv); err != nil {
			return nil, err
		}
		if si.ExpiresAt < now || sv != ver {
			continue // 过期/被吊销
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// LogoutAll 全设备登出：token_version+1（所有在途 JWT 即刻失效）+ 台账清理。
func (s *sqliteStore) LogoutAll(ctx context.Context, username string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET token_version = token_version + 1, updated_at = datetime('now')
		WHERE username = ?`, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: user %s", ErrNotFound, username)
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE username = ?`, username)
	return nil
}
