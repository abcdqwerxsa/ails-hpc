package store

import (
	"context"
	"encoding/json"
	"io"
)

// Seeds 是集群侧供给种子（-export-seeds 输出 / entrypoint 读取）：
// Tenants 驱动父账号创建（parent_account=slug，Phase 5 fairshare 层级），
// Users 驱动 unix 账号 + 叶子账号（account parent=<租户父账号>）+ association。
type Seeds struct {
	Tenants []SeedTenant `json:"tenants"`
	Users   []SeedUser   `json:"users"`
}

type SeedTenant struct {
	Slug          string `json:"slug"`
	ParentAccount string `json:"parentAccount"`
	Status        string `json:"status"`
}

type SeedUser struct {
	Username   string `json:"username"`
	ClusterUser string `json:"clusterUser"`
	UID        int    `json:"uid"`
	GID        int    `json:"gid"`
	Account    string `json:"account"`
	TenantSlug string `json:"tenantSlug"`
}

// ExportSeeds 把用户库导出为集群供给种子 JSON（幂等快照；db 为真相源时的重建路径）。
func ExportSeeds(st Store) (*Seeds, error) {
	tenants, err := st.Tenants(context.Background())
	if err != nil {
		return nil, err
	}
	sd := &Seeds{Tenants: []SeedTenant{}, Users: []SeedUser{}}
	for _, t := range tenants {
		sd.Tenants = append(sd.Tenants, SeedTenant{
			Slug: t.Slug, ParentAccount: t.ParentAccount, Status: t.Status,
		})
	}
	for _, u := range st.ListUsers() {
		sd.Users = append(sd.Users, SeedUser{
			Username: u.Username, ClusterUser: u.ClusterUser,
			UID: u.UID, GID: u.GID, Account: u.Account, TenantSlug: u.TenantSlug,
		})
	}
	return sd, nil
}

// WriteSeedsJSON 导出并写为缩进 JSON（main 的 -export-seeds 用）。
func WriteSeedsJSON(w io.Writer, st Store) error {
	sd, err := ExportSeeds(st)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(sd)
}
