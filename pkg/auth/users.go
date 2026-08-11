package auth

import (
	"errors"
	"os"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// ErrInvalidCredentials 登录失败（用户不存在或密码错误）。
// 两种情况返回同一错误，避免用户名枚举。
var ErrInvalidCredentials = errors.New("invalid username or password")

// User 表示一个可登录账号。PasswordHash 为 bcrypt 哈希；Role 为权威角色。
// JSON tag 对 password_hash 标 `-`，确保哈希永不序列化进响应。
type User struct {
	Username     string `yaml:"username"      json:"username"`
	PasswordHash string `yaml:"password_hash" json:"-"`
	Role         string `yaml:"role"          json:"role"`
	OrgSlug      string `yaml:"orgSlug"       json:"orgSlug"`
	TenantNS     string `yaml:"tenantNs"      json:"tenantNs"`
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
}

type userStoreImpl struct {
	users map[string]*User
}

// NewUserStoreFromList 从内存用户列表构造 UserStore（测试与开发种子用）。
func NewUserStoreFromList(users []User) UserStore {
	m := make(map[string]*User, len(users))
	for i := range users {
		u := users[i]
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
	return NewUserStoreFromList(uf.Users), nil
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
