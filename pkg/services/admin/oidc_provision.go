package admin

// auth.OIDCProvisioner 的 service 层实现：store 落数之上追加 Slurm 供给（JIT 用户
// 也要有集群账号才能提交作业）与审计。编译期钉接口，防漂移。

import (
	"context"
	"fmt"

	"ails-hpc/pkg/auth"
)

var _ auth.OIDCProvisioner = (*Service)(nil)

// UserByOIDCSub 按绑定的 SSO 身份查用户（只读，无库时 false）。
func (s *Service) UserByOIDCSub(sub string) (*auth.User, bool) {
	if err := s.ensure(); err != nil {
		return nil, false
	}
	return s.st.UserByOIDCSub(sub)
}

// LinkOIDC 绑定 sub 到本地账号 + 审计。
func (s *Service) LinkOIDC(username, sub string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if err := s.st.LinkOIDC(username, sub); err != nil {
		return err
	}
	_ = s.st.WriteAudit(context.Background(), username, "user.oidc.link", "user:"+username, "", `{}`)
	return nil
}

// UnlinkOIDC 解绑 + 审计（auth_source=oidc 的账号在 store 层被拒——防自锁）。
func (s *Service) UnlinkOIDC(username string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if err := s.st.UnlinkOIDC(username); err != nil {
		return err
	}
	_ = s.st.WriteAudit(context.Background(), username, "user.oidc.unlink", "user:"+username, "", `{}`)
	return nil
}

// ProvisionOIDCUser JIT 开户：store 落库 → Slurm 供给（失败明确报错，DB 已提交可重试）
// → 审计。
func (s *Service) ProvisionOIDCUser(username, email, displayName, roleName, tenantSlug, sub string) (*auth.User, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	u, err := s.st.ProvisionOIDCUser(username, email, displayName, roleName, tenantSlug, sub)
	if err != nil {
		return nil, err
	}
	if err := s.provisionUser(context.Background(), u); err != nil {
		return u, fmt.Errorf("%w: %v", ErrProvisionFailed, err)
	}
	_ = s.st.WriteAudit(context.Background(), username, "user.oidc.provision", "user:"+username, "",
		fmt.Sprintf(`{"role":%q,"tenant":%q,"auth_source":"oidc"}`, roleName, tenantSlug))
	return u, nil
}
