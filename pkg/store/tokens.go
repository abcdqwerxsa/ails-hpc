package store

// T1 个人 API token 的 sqlite 实现。令牌形态 `ailspat_<43 字符 base64url>`（32 字节
// crypto/rand）；库内只存 sha256 hex（高熵随机值无需慢哈希，唯一索引直查），明文仅
// 签发响应出现一次。接口面定义在 pkg/auth/pat.go（防 import 环），此处结构满足。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ails-hpc/pkg/auth"
)

// MaxAPITokensPerUser 单用户活跃（未吊销）令牌上限。配额错误以 auth.ErrTokenQuota
// 返回（auth 侧 errors.Is 判定——防 import 环的映射锚）。
const MaxAPITokensPerUser = 10

// 编译期钉住：sqlite 库满足 auth 的 PAT 管理面。
var _ auth.PATManager = (*sqliteStore)(nil)

func (s *sqliteStore) CreateAPIToken(ctx context.Context, username, name, tokenHash, prefix, expiresAt string) (int64, error) {
	var active int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_tokens WHERE username = ? AND revoked_at IS NULL`,
		username).Scan(&active); err != nil {
		return 0, err
	}
	if active >= MaxAPITokensPerUser {
		return 0, auth.ErrTokenQuota
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO api_tokens (username, name, token_hash, prefix, expires_at)
		VALUES (?, ?, ?, ?, NULLIF(?, ''))`, username, name, tokenHash, prefix, expiresAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *sqliteStore) ListAPITokens(ctx context.Context, username string) ([]auth.PATInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, prefix, created_at, COALESCE(last_used_at, ''),
		       COALESCE(expires_at, ''), revoked_at IS NOT NULL
		FROM api_tokens WHERE username = ? ORDER BY id DESC`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []auth.PATInfo{}
	for rows.Next() {
		var t auth.PATInfo
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.Revoked); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *sqliteStore) RevokeAPIToken(ctx context.Context, username string, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE api_tokens SET revoked_at = datetime('now')
		WHERE id = ? AND username = ? AND revoked_at IS NULL`, id, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: token %d", ErrNotFound, id) // 非本人/已吊销/不存在统一 404（防枚举）
	}
	return nil
}

func (s *sqliteStore) LookupAPIToken(tokenHash string) (auth.PATRecord, error) {
	var r auth.PATRecord
	var revoked sql.NullString
	err := s.db.QueryRow(`
		SELECT id, username, COALESCE(expires_at, ''), COALESCE(last_used_at, ''), revoked_at
		FROM api_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&r.ID, &r.Username, &r.ExpiresAt, &r.LastUsedAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: token", ErrNotFound)
	}
	if err != nil {
		return r, err
	}
	r.Revoked = revoked.Valid
	return r, nil
}

// TouchAPIToken 更新 last_used_at（调用方已做 ≥5 分钟节流）。
func (s *sqliteStore) TouchAPIToken(id int64) error {
	_, err := s.db.Exec(`UPDATE api_tokens SET last_used_at = datetime('now') WHERE id = ?`, id)
	return err
}
