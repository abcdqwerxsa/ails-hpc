package admin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"ails-hpc/pkg/slurmrest"
)

// 预约与 QOS 管理（roadmap 4.2）：admin 直通 scontrol/sacctmgr。
// 21.08 无对应 REST 端点（节点/预约写均缺），沿用 CLI 教义；runner 可注入测试。

// clusterRunner 是集群管理命令执行面（scontrol/sacctmgr）。
type clusterRunner func(args ...string) ([]byte, error)

var defaultClusterRunner clusterRunner = slurmrest.RunInSlurmctld

// Reservation 是一条 Slurm 预约（scontrol show reserv 解析）。
type Reservation struct {
	Name      string `json:"name"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Duration  string `json:"duration"`
	Nodes     string `json:"nodes"`
	Users     string `json:"users"`
	Accounts  string `json:"accounts,omitempty"`
	State     string `json:"state,omitempty"` // ACTIVE/INACTIVE
}

// QOS 是一条 Slurm QOS（sacctmgr show qos -P 解析）。
type QOS struct {
	Name      string `json:"name"`
	Priority  string `json:"priority,omitempty"`
	GrpTRES   string `json:"grp_tres,omitempty"`
	MaxTRES   string `json:"max_tres,omitempty"`
	MaxWall   string `json:"max_wall,omitempty"`
	MaxJobs   string `json:"max_jobs,omitempty"`
}

// nameRE 予約/QOS 名白名单。
var nameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// ListReservations scontrol show reserv（空列表=无预约）。
func (s *Service) ListReservations(ctx context.Context) ([]Reservation, error) {
	out, err := s.runCluster("scontrol", "show", "reserv")
	if err != nil {
		return nil, fmt.Errorf("scontrol show reserv: %w", err)
	}
	return parseReservations(string(out)), nil
}

// CreateReservation scontrol create reservation。startTime 为 "YYYY-MM-DDTHH:MM" 或空=now+1min。
func (s *Service) CreateReservation(ctx context.Context, name, startTime string, durationMin int, nodes, users, partition string) (*Reservation, error) {
	if !nameRE.MatchString(name) || durationMin <= 0 || durationMin > 30*24*60 {
		return nil, fmt.Errorf("invalid reservation name/duration")
	}
	if startTime == "" {
		startTime = time.Now().Add(time.Minute).Format("2006-01-02T15:04")
	}
	args := []string{"scontrol", "create", "reservation",
		"reservationname=" + name,
		"starttime=" + startTime,
		fmt.Sprintf("duration=%d", durationMin),
	}
	if nodes != "" {
		args = append(args, "nodes="+nodes)
	}
	if partition != "" {
		args = append(args, "partition="+partition)
	}
	if users != "" {
		args = append(args, "users="+users)
	}
	if _, err := s.runCluster(args...); err != nil {
		return nil, fmt.Errorf("scontrol create reservation: %w", err)
	}
	res, _ := s.findReservation(ctx, name)
	return res, nil
}

// DeleteReservation scontrol delete reservation <name>。
func (s *Service) DeleteReservation(ctx context.Context, name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid reservation name")
	}
	if _, err := s.runCluster("scontrol", "delete", "reservation="+name); err != nil {
		return fmt.Errorf("scontrol delete reservation: %w", err)
	}
	return nil
}

func (s *Service) findReservation(ctx context.Context, name string) (*Reservation, error) {
	rs, err := s.ListReservations(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rs {
		if rs[i].Name == name {
			return &rs[i], nil
		}
	}
	return nil, ErrReservationNotFound
}

// parseReservations 解析 scontrol show reserv 的多块输出。
func parseReservations(out string) []Reservation {
	res := []Reservation{}
	for _, block := range strings.Split(out, "\n\n") {
		if !strings.Contains(block, "ReservationName=") {
			continue
		}
		kv := map[string]string{}
		for _, ln := range strings.Split(block, "\n") {
			for _, f := range strings.Fields(ln) {
				if i := strings.Index(f, "="); i > 0 {
					kv[f[:i]] = f[i+1:]
				}
			}
		}
		if kv["ReservationName"] == "" {
			continue
		}
		res = append(res, Reservation{
			Name:      kv["ReservationName"],
			StartTime: kv["StartTime"],
			EndTime:   kv["EndTime"],
			Duration:  kv["Duration"],
			Nodes:     kv["Nodes"],
			Users:     kv["Users"],
			Accounts:  kv["Accounts"],
			State:     strings.TrimSuffix(strings.TrimPrefix(kv["State"], "("), ")"),
		})
	}
	return res
}

// ListQOS sacctmgr -nP show qos。
func (s *Service) ListQOS(ctx context.Context) ([]QOS, error) {
	out, err := s.runCluster("sh", "-c",
		`sacctmgr -nP show qos format=name,priority,grptres,maxtrespu,maxwallpu,grpj || sacctmgr -nP show qos format=name,priority,grptres,maxtres,maxwall,grpj`)
	if err != nil {
		return nil, fmt.Errorf("sacctmgr show qos: %w", err)
	}
	qos := []QOS{}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(ln, "|")
		if len(f) < 1 || f[0] == "" {
			continue
		}
		q := QOS{Name: f[0]}
		get := func(i int) string {
			if i < len(f) && f[i] != "" {
				return f[i]
			}
			return ""
		}
		q.Priority = get(1)
		q.GrpTRES = get(2)
		q.MaxTRES = get(3)
		q.MaxWall = get(4)
		q.MaxJobs = get(5)
		qos = append(qos, q)
	}
	return qos, nil
}

// CreateQOS sacctmgr add qos <name>（可选直接带 GrpTRES）。幂等。
func (s *Service) CreateQOS(ctx context.Context, name, grpTRES string) (*QOS, error) {
	if !nameRE.MatchString(name) {
		return nil, fmt.Errorf("invalid qos name")
	}
	args := []string{"sacctmgr", "-i", "add", "qos", name}
	if grpTRES != "" && limitRE.MatchString(grpTRES) {
		args = append(args, "grptres="+grpTRES)
	}
	if _, err := s.runCluster(args...); err != nil {
		return nil, fmt.Errorf("sacctmgr add qos: %w", err)
	}
	qos, _ := s.ListQOS(ctx)
	for i := range qos {
		if qos[i].Name == name {
			return &qos[i], nil
		}
	}
	return &QOS{Name: name}, nil
}

// SetTenantQOS 把 QOS 绑到租户父账号（sacctmgr modify account <parent> set qos=...）。
func (s *Service) SetTenantQOS(ctx context.Context, tenantSlug, qosName string) error {
	if !nameRE.MatchString(qosName) {
		return fmt.Errorf("invalid qos name")
	}
	t, err := s.st.TenantBySlug(ctx, tenantSlug)
	if err != nil {
		return err
	}
	if _, err := s.runCluster("sacctmgr", "-i", "modify", "account", t.ParentAccount, "set", "qos="+qosName); err != nil {
		return fmt.Errorf("sacctmgr modify account qos: %w", err)
	}
	return nil
}

// runCluster 执行集群管理命令（默认 slurmctld CLI；测试可换 Service.runner——nil 即默认）。
func (s *Service) runCluster(args ...string) ([]byte, error) {
	r := s.runner
	if r == nil {
		r = defaultClusterRunner
	}
	return r(args...)
}
