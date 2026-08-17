package store

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaMigrations 是有序迁移列表；每条对应一个版本号（=下标+1）。
// migrations 表记录已应用版本，Open 时按序补齐（幂等）。
var schemaMigrations = []string{
	// v1：多租户基础三表（设计 §3）。
	`
CREATE TABLE IF NOT EXISTS tenants (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  slug           TEXT NOT NULL UNIQUE,
  name           TEXT NOT NULL DEFAULT '',
  parent_account TEXT NOT NULL UNIQUE,
  status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
  created_at     TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at     TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL CHECK (role IN ('admin','ops_admin','tenant_admin','member')),
  tenant_id     INTEGER NOT NULL REFERENCES tenants(id),
  cluster_user  TEXT NOT NULL UNIQUE,
  uid           INTEGER NOT NULL UNIQUE,
  gid           INTEGER NOT NULL DEFAULT 2000,
  account       TEXT NOT NULL UNIQUE,
  display_name  TEXT NOT NULL DEFAULT '',
  email         TEXT NOT NULL DEFAULT '',
  auth_source   TEXT NOT NULL DEFAULT 'local' CHECK (auth_source IN ('local','ldap')),
  status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  token_version INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id, status);
CREATE TABLE IF NOT EXISTS audit_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  actor      TEXT NOT NULL,
  action     TEXT NOT NULL,
  target     TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '{}',
  request_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);`,
	// v2(C1/C2):外键开启前的存量孤儿清洗(users.tenant_id → tenants);
	// audit_log(actor) 查询索引;users/tenants 的 updated_at 统一触发器维护。
	`
DELETE FROM users WHERE tenant_id NOT IN (SELECT id FROM tenants);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log(actor, id);
CREATE TRIGGER IF NOT EXISTS trg_users_updated_at
  AFTER UPDATE ON users FOR EACH ROW
  BEGIN
    UPDATE users SET updated_at = datetime('now') WHERE id = OLD.id;
  END;
CREATE TRIGGER IF NOT EXISTS trg_tenants_updated_at
  AFTER UPDATE ON tenants FOR EACH ROW
  BEGIN
    UPDATE tenants SET updated_at = datetime('now') WHERE id = OLD.id;
  END;`,
}

// migrate 按序应用未执行的迁移（幂等；并发安全由 sqlite 单写者 + busy_timeout 保证）。
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}

	var current int
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}

	for i, stmt := range schemaMigrations {
		v := i + 1
		if v <= current {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migration v%d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, v); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: record migration v%d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
