package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// ErrInvalidCredentials 登录失败（用户不存在或密码错误）。
// 两种情况返回同一错误，避免用户名枚举。
var ErrInvalidCredentials = errors.New("invalid username or password")

// User 表示一个可登录账号。PasswordHash 为 bcrypt 哈希；Role 为权威角色。
// JSON tag 对 password_hash 标 `-`，确保哈希永不序列化进响应。
//
// ClusterUser/UID/GID/Account 为真·每用户 Slurm 隔离（L1+L3）所需：ClusterUser 是
// 集群 unix 身份，Account 是 Slurm 账号（约定 == ClusterUser）。部署时按本字段在所有
// 节点 useradd + sacctmgr 建 account/association。
type User struct {
	Username     string `yaml:"username"      json:"username"`
	PasswordHash string `yaml:"password_hash" json:"-"`
	Role         string `yaml:"role"          json:"role"`
	OrgSlug      string `yaml:"orgSlug"       json:"orgSlug"`
	TenantNS     string `yaml:"tenantNs"      json:"tenantNs"`
	ClusterUser  string `yaml:"clusterUser"   json:"clusterUser"`
	UID          int    `yaml:"uid"           json:"uid"`
	GID          int    `yaml:"gid"           json:"gid"`
	Account      string `yaml:"account"       json:"account"`
	// TenantSlug 所属租户（多租户 Phase 1）：DB 库映射 tenants.slug；yaml 库 = OrgSlug。
	TenantSlug string `yaml:"tenantSlug,omitempty" json:"tenantSlug"`
	// Status 账号状态（active|disabled）；yaml 库恒 active。
	Status string `yaml:"status,omitempty" json:"status,omitempty"`
	// TokenVersion 令牌版本：改密/禁用 +1 → 在途 JWT 即刻失效（中间件按请求比对 claims.Ver）。
	TokenVersion int `yaml:"-" json:"-"`
}

// CompareHashAndPassword 是 bcrypt 校验的薄导出（pkg/store 等同面实现复用）。
func CompareHashAndPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// BcryptGenerateFromPassword 是 bcrypt 加密的薄导出（导入/测试用；MinCost 由调用方决定）。
func BcryptGenerateFromPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	return string(h), err
}

// normalize 填充派生默认值（yaml 库与导入共用：租户回退 orgSlug、状态默认 active）。
func (u *User) normalize() {
	if u.TenantSlug == "" {
		u.TenantSlug = u.OrgSlug
	}
	if u.Status == "" {
		u.Status = "active"
	}
}

// usersFile 对应 config/users.yaml 的磁盘结构。
type usersFile struct {
	Users []User `yaml:"users"`
}

// UserStore 解析用户凭证的只读仓库接口。
//
// 本轮（服务端权威角色）仅为内存/yaml 支撑；真多租户用户库（DB/LDAP/SSO）
// 与 per-user/租户隔离在路线图中。
type UserStore interface {
	// Lookup 按用户名查找用户（不做凭证校验）。
	Lookup(username string) (*User, bool)
	// Verify 校验用户名+密码，成功返回该用户，失败返回 ErrInvalidCredentials。
	Verify(username, password string) (*User, error)
	// ListUsers 全量用户（多租户 scope 解析租户成员用；DB 库未来同面实现）。
	ListUsers() []*User
	// SetPassword 更新密码哈希并 bump TokenVersion（在途令牌即刻失效）。
	// 文件支撑的 yaml 库会外科式改写 password_hash 行（保留其余内容与注释）；
	// 纯内存库（NewUserStoreFromList）仅更新内存。用户不存在返回 ErrInvalidCredentials。
	SetPassword(username, bcryptHash string) error
	// UserVersion 返回用户当前 TokenVersion（中间件比对 claims.Ver 用）。
	UserVersion(username string) (int, bool)
}

type userStoreImpl struct {
	users map[string]*User
	path  string // 非空 = 来自 yaml 文件：SetPassword 时外科式回写（保留其余行与注释）
}

// NewUserStoreFromList 从内存用户列表构造 UserStore（测试与开发种子用）。
func NewUserStoreFromList(users []User) UserStore {
	m := make(map[string]*User, len(users))
	for i := range users {
		u := users[i]
		u.normalize() // 派生默认值：TenantSlug←OrgSlug、Status←active
		m[u.Username] = &u
	}
	return &userStoreImpl{users: m}
}

// LoadUserStore 从 YAML 文件（默认 config/users.yaml）加载用户库。
func LoadUserStore(path string) (UserStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var uf usersFile
	if err := yaml.Unmarshal(data, &uf); err != nil {
		return nil, err
	}
	st := NewUserStoreFromList(uf.Users).(*userStoreImpl)
	st.path = path
	return st, nil
}

// SetPassword 更新哈希并 bump TokenVersion。文件支撑时外科式改写该用户的
// password_hash 行——只动这一行的值，文件其余内容（含头部注释）原样保留。
func (s *userStoreImpl) SetPassword(username, bcryptHash string) error {
	u, ok := s.users[username]
	if !ok {
		return ErrInvalidCredentials
	}
	u.PasswordHash = bcryptHash
	u.TokenVersion++
	if s.path == "" {
		return nil // 纯内存库（测试）
	}
	return rewriteYamlPassword(s.path, username, bcryptHash)
}

// UserVersion 返回用户当前 TokenVersion。
func (s *userStoreImpl) UserVersion(username string) (int, bool) {
	u, ok := s.users[username]
	if !ok {
		return 0, false
	}
	return u.TokenVersion, true
}

// rewriteYamlPassword 定位 "- username: <name>" 块内的 password_hash 行并替换其值。
// 不重排/不重序列化整个文件 → 注释与字段顺序不丢。
func rewriteYamlPassword(path, username, newHash string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	inBlock := false
	replaced := false
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "- username:") {
			inBlock = strings.TrimSpace(strings.TrimPrefix(t, "- username:")) == username
			continue
		}
		if inBlock && strings.HasPrefix(t, "password_hash:") {
			indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
			lines[i] = indent + `password_hash: "` + newHash + `"`
			replaced = true
			break
		}
	}
	if !replaced {
		return fmt.Errorf("users.yaml: password_hash line not found for %s", username)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *userStoreImpl) ListUsers() []*User {
	out := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	return out
}

func (s *userStoreImpl) Lookup(username string) (*User, bool) {
	u, ok := s.users[username]
	return u, ok
}

func (s *userStoreImpl) Verify(username, password string) (*User, error) {
	u, ok := s.users[username]
	if !ok {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	out := *u // 返回副本，避免调用方改写库内对象
	return &out, nil
}
