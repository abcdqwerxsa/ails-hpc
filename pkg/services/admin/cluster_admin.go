package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ails-hpc/pkg/auth"
	"ails-hpc/pkg/slurmrest"
	"ails-hpc/pkg/store"
)

// 预约与 QOS 管理（roadmap 4.2）：admin 直通 scontrol/sacctmgr。
// 21.08 无对应 REST 端点（节点/预约写均缺），沿用 CLI 教义；runner 可注入测试。

// clusterRunner 是集群管理命令执行面（scontrol/sacctmgr）。
type clusterRunner func(args ...string) ([]byte, error)

// ClusterRunner 是 clusterRunner 的导出别名——cmd/apiserver 测试装配可注入假实现。
type ClusterRunner = clusterRunner

var defaultClusterRunner clusterRunner = slurmrest.RunInSlurmctld

// SetClusterRunner 注入集群命令执行面（测试装配用；生产默认 slurmctld CLI）。
func (s *Service) SetClusterRunner(r ClusterRunner) { s.runner = r }

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

// ErrQOSNotFound QOS 不存在。
var ErrQOSNotFound = errors.New("admin: qos not found")

// QOS 是一条 Slurm QOS 完整属性对象（sacctmgr show qos -nP 解析）。
type QOS struct {
	Name                 string `json:"name"`
	Description          string `json:"description,omitempty"`
	Priority             string `json:"priority,omitempty"`
	GrpTRES              string `json:"grp_tres,omitempty"`
	MaxTRES              string `json:"max_tres,omitempty"`
	MaxTRESPerUser       string `json:"max_tres_per_user,omitempty"`
	MaxJobs              string `json:"max_jobs,omitempty"`
	MaxJobsPerUser       string `json:"max_jobs_per_user,omitempty"`
	MaxSubmitJobsPerUser string `json:"max_submit_jobs_per_user,omitempty"`
	MaxWall              string `json:"max_wall,omitempty"`
	MaxWallDuration      string `json:"max_wall_duration,omitempty"`
}

// QOSUpdates 是 QOS 可设置与修改的字段集合（空串表示不设置/不变更）。
type QOSUpdates struct {
	Description          string `json:"description,omitempty"`
	Priority             string `json:"priority,omitempty"`
	GrpTRES              string `json:"grpTRES,omitempty"`
	MaxTRES              string `json:"maxTRES,omitempty"`
	MaxTRESPerUser       string `json:"maxTRESPerUser,omitempty"`
	MaxJobs              string `json:"maxJobs,omitempty"`
	MaxJobsPerUser       string `json:"maxJobsPerUser,omitempty"`
	MaxSubmitJobsPerUser string `json:"maxSubmitJobsPerUser,omitempty"`
	MaxWall              string `json:"maxWall,omitempty"`
	MaxWallDuration      string `json:"maxWallDuration,omitempty"`
}

// QOSParams 是 QOSUpdates 的别名。
type QOSParams = QOSUpdates

// UnmarshalJSON 兼容 camelCase、snake_case 及缩写别名。
func (u *QOSUpdates) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	getString := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k]; ok && v != nil {
				if s, ok := v.(string); ok {
					return strings.TrimSpace(s)
				}
				return fmt.Sprintf("%v", v)
			}
		}
		return ""
	}
	u.Description = getString("description", "Description")
	u.Priority = getString("priority", "Priority")
	u.GrpTRES = getString("grpTRES", "grp_tres", "GrpTRES")
	u.MaxTRES = getString("maxTRES", "max_tres", "MaxTRES")
	u.MaxTRESPerUser = getString("maxTRESPerUser", "max_tres_per_user", "max_tres_pu", "maxTRESPU", "MaxTRESPerUser")
	if u.MaxTRESPerUser == "" {
		u.MaxTRESPerUser = u.MaxTRES
	}
	u.MaxJobs = getString("maxJobs", "max_jobs", "MaxJobs")
	u.MaxJobsPerUser = getString("maxJobsPerUser", "max_jobs_per_user", "max_jobs_pu", "maxJobsPU", "MaxJobsPerUser")
	if u.MaxJobsPerUser == "" {
		u.MaxJobsPerUser = u.MaxJobs
	}
	u.MaxSubmitJobsPerUser = getString("maxSubmitJobsPerUser", "max_submit_jobs_per_user", "max_submit_pu", "maxSubmitPU", "MaxSubmitJobsPerUser")
	u.MaxWall = getString("maxWall", "max_wall", "MaxWall", "maxWallDuration", "max_wall_duration", "MaxWallDuration")
	u.MaxWallDuration = u.MaxWall
	return nil
}

// unixSafeRE 是 unix 用户名/Slurm 账号的安全字符集：小写字母或下划线开头，仅 [a-z0-9_-]，至多 32 字符。
var unixSafeRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// UserQOSInfo 表示用户在 Slurm 中的 QOS 关联详情（默认 QOS 与可用 QOS 清单）。
type UserQOSInfo struct {
	Username    string   `json:"username"`
	ClusterUser string   `json:"clusterUser"`
	Account     string   `json:"account"`
	TenantSlug  string   `json:"tenantSlug"`
	DefaultQOS  string   `json:"defaultQos"`
	AllowedQOS  []string `json:"allowedQos"`
}

// UserQOS 是 UserQOSInfo 的别名。
type UserQOS = UserQOSInfo

// UserQOSUpdates 包含修改用户 QOS 关联的字段。
type UserQOSUpdates struct {
	DefaultQOS string   `json:"defaultQos"`
	AllowedQOS []string `json:"allowedQos"`
}

// UnmarshalJSON 兼容 camelCase、PascalCase、snake_case 及缩写别名。
func (u *UserQOSUpdates) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	getString := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k]; ok && v != nil {
				if s, ok := v.(string); ok {
					return strings.TrimSpace(s)
				}
				return fmt.Sprintf("%v", v)
			}
		}
		return ""
	}
	u.DefaultQOS = getString("defaultQos", "defaultQOS", "default_qos", "defaultqos", "DefaultQOS", "DefaultQos")

	getStringSlice := func(keys ...string) []string {
		for _, k := range keys {
			if v, ok := raw[k]; ok && v != nil {
				if slice, ok := v.([]any); ok {
					res := make([]string, 0, len(slice))
					for _, item := range slice {
						if itemStr, ok := item.(string); ok {
							res = append(res, strings.TrimSpace(itemStr))
						}
					}
					return res
				}
				if str, ok := v.(string); ok {
					parts := strings.Split(str, ",")
					res := make([]string, 0, len(parts))
					for _, p := range parts {
						if tr := strings.TrimSpace(p); tr != "" {
							res = append(res, tr)
						}
					}
					return res
				}
			}
		}
		return nil
	}
	u.AllowedQOS = getStringSlice("allowedQos", "allowedQOS", "allowed_qos", "allowedqos", "AllowedQOS", "AllowedQos")
	return nil
}

// AvailableQOSResponse 包含当前用户可用的 QOS 完整属性对象列表与默认 QOS。
type AvailableQOSResponse struct {
	DefaultQOS   string `json:"defaultQos"`
	AllowedQOS   []QOS  `json:"allowedQos"`
	AvailableQOS []QOS  `json:"availableQos,omitempty"`
}

// ValidateUserQOSUpdates 校验更新载荷。
func ValidateUserQOSUpdates(u *UserQOSUpdates) error {
	u.DefaultQOS = strings.TrimSpace(u.DefaultQOS)
	cleanAllowed := make([]string, 0, len(u.AllowedQOS))
	seen := make(map[string]bool)
	for _, q := range u.AllowedQOS {
		trimmed := strings.TrimSpace(q)
		if trimmed != "" && !seen[trimmed] {
			seen[trimmed] = true
			cleanAllowed = append(cleanAllowed, trimmed)
		}
	}
	u.AllowedQOS = cleanAllowed

	if u.DefaultQOS == "" && len(u.AllowedQOS) == 0 {
		return errors.New("at least one of defaultQos or allowedQos must be provided")
	}
	if u.DefaultQOS != "" && u.DefaultQOS != "-1" && !qosNameRE.MatchString(u.DefaultQOS) {
		return fmt.Errorf("invalid defaultQos %q", u.DefaultQOS)
	}
	for _, q := range u.AllowedQOS {
		if q != "-1" && !qosNameRE.MatchString(q) {
			return fmt.Errorf("invalid qos name %q", q)
		}
	}
	if u.DefaultQOS != "" && len(u.AllowedQOS) > 0 {
		found := false
		for _, q := range u.AllowedQOS {
			if q == u.DefaultQOS {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("defaultQos %q must be included in allowedQos", u.DefaultQOS)
		}
	}
	return nil
}

// qosNameRE QOS 名称白名单：英文字母开头，允许字母、数字、下划线、中划线，长度 1-32。
var qosNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// qosValueREs 逐字段安全白名单校验（防范 Shell 注入，严格遵从 Slurm 语法）：
// - Description: 中英文、数字、常见安全符号（长度 1-128，禁止引号、反引号、$、分号、换行等）
// - Priority: 整数 0 到 4294967295 或 -1
// - GrpTRES: cpu=N, mem=NG, gres/gpu=N, gres/gpu:model=N 等逗号分隔格式，或 -1
// - MaxTRESPerUser: 结构同 GrpTRES
// - MaxJobsPerUser: 整数或 UNLIMITED 或 -1
// - MaxSubmitJobsPerUser: 整数或 UNLIMITED 或 -1
// - MaxWallDuration: 分钟数、MM:SS、HH:MM:SS、D-HH:MM:SS 或 UNLIMITED 或 -1
var qosValueREs = map[string]*regexp.Regexp{
	"Description":          regexp.MustCompile(`^[0-9A-Za-z\p{Han} .,()_\-\[\]/:+]{1,128}$`),
	"Priority":             regexp.MustCompile(`^(-1|0|[1-9][0-9]{0,9})$`),
	"GrpTRES":              regexp.MustCompile(`^([a-zA-Z0-9_/-]+(:[a-zA-Z0-9_-]+)?=[0-9]+[KMGTkmgt]?(,[a-zA-Z0-9_/-]+(:[a-zA-Z0-9_-]+)?=[0-9]+[KMGTkmgt]?)*|-1)$`),
	"MaxTRESPerUser":       regexp.MustCompile(`^([a-zA-Z0-9_/-]+(:[a-zA-Z0-9_-]+)?=[0-9]+[KMGTkmgt]?(,[a-zA-Z0-9_/-]+(:[a-zA-Z0-9_-]+)?=[0-9]+[KMGTkmgt]?)*|-1)$`),
	"MaxJobsPerUser":       regexp.MustCompile(`^(?i)(UNLIMITED|-1|0|[1-9][0-9]{0,7})$`),
	"MaxSubmitJobsPerUser": regexp.MustCompile(`^(?i)(UNLIMITED|-1|0|[1-9][0-9]{0,7})$`),
	"MaxWallDuration":      regexp.MustCompile(`^(?i)(UNLIMITED|-1|[0-9]{1,6}|[0-9]{1,4}:[0-5][0-9](:[0-5][0-9])?|[0-9]{1,3}-[0-9]{1,2}(:[0-5][0-9](:[0-5][0-9])?)?)$`),
}

var qosFields = []struct {
	Key     string // sacctmgr 命令行参数名
	JSONKey string
	Get     func(QOSUpdates) string
}{
	{"Description", "description", func(u QOSUpdates) string { return u.Description }},
	{"Priority", "priority", func(u QOSUpdates) string { return u.Priority }},
	{"GrpTRES", "grpTRES", func(u QOSUpdates) string { return u.GrpTRES }},
	{"MaxTRESPerUser", "maxTRESPerUser", func(u QOSUpdates) string {
		if u.MaxTRESPerUser != "" {
			return u.MaxTRESPerUser
		}
		return u.MaxTRES
	}},
	{"MaxJobsPerUser", "maxJobsPerUser", func(u QOSUpdates) string {
		if u.MaxJobsPerUser != "" {
			return u.MaxJobsPerUser
		}
		return u.MaxJobs
	}},
	{"MaxSubmitJobsPerUser", "maxSubmitJobsPerUser", func(u QOSUpdates) string { return u.MaxSubmitJobsPerUser }},
	{"MaxWallDuration", "maxWallDuration", func(u QOSUpdates) string {
		if u.MaxWallDuration != "" {
			return u.MaxWallDuration
		}
		return u.MaxWall
	}},
}

// ValidateQOSFields 校验所填写的 QOS 字段值是否合法（用于 CreateQOS，允许字段为空）。
func ValidateQOSFields(u QOSUpdates) error {
	for _, f := range qosFields {
		v := strings.TrimSpace(f.Get(u))
		if v == "" {
			continue
		}
		if re, ok := qosValueREs[f.Key]; ok && !re.MatchString(v) {
			return fmt.Errorf("invalid %s value %q", f.Key, v)
		}
	}
	return nil
}

// ValidateQOSUpdates 校验更新载荷（用于 UpdateQOS，至少需修改一个字段且各字段合法）。
func ValidateQOSUpdates(u QOSUpdates) error {
	set := 0
	for _, f := range qosFields {
		v := strings.TrimSpace(f.Get(u))
		if v == "" {
			continue
		}
		set++
		if re, ok := qosValueREs[f.Key]; ok && !re.MatchString(v) {
			return fmt.Errorf("invalid %s value %q", f.Key, v)
		}
	}
	if set == 0 {
		return fmt.Errorf("no qos fields to update")
	}
	return nil
}

// nameRE 予約/QOS 名白名单。
var nameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// 安全审计 2026-08-19 P0-2：预约 spec 各字段以 '...' 朴素包裹拼进容器内 sh -c，
// 内含单引号即逃逸（root 注入）。以下白名单从源头禁掉引号/元字符（与 UpdatePartition
// 的逐字段 regex 同教义）：
//   - resvTimeRE：scontrol starttime，固定 YYYY-MM-DDTHH:MM
//   - resvNodesRE：Slurm 节点表表达式（node1 / node[2-3],node1 等）
//   - resvUsersRE：逗号分隔 unix 用户名
//   - resvPartRE：分区名
var (
	resvTimeRE  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$`)
	resvNodesRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_,\[\]\-]{0,127}$`)
	resvUsersRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}(,[a-z_][a-z0-9_-]{0,31})*$`)
	resvPartRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)
)

// ListReservations scontrol show reserv（空列表=无预约）。
func (s *Service) ListReservations(ctx context.Context) ([]Reservation, error) {
	out, err := s.runCluster("scontrol", "show", "reserv")
	if err != nil {
		return nil, fmt.Errorf("scontrol show reserv: %w", err)
	}
	return parseReservations(string(out)), nil
}

// CreateReservation scontrol create reservation。startTime 为 "YYYY-MM-DDTHH:MM" 或空=now+1min。
// v3-X1：成功后落审计（st 为 nil 的 yaml 模式跳过——纯集群操作不依赖用户库）。
func (s *Service) CreateReservation(ctx context.Context, actor, name, startTime string, durationMin int, nodes, users, partition, rid string) (*Reservation, error) {
	if !nameRE.MatchString(name) || durationMin <= 0 || durationMin > 30*24*60 {
		return nil, fmt.Errorf("invalid reservation name/duration")
	}
	if startTime == "" {
		startTime = time.Now().Add(time.Minute).Format("2006-01-02T15:04")
	}
	// P0-2：spec 四字段白名单（用户可控且会被拼进 sh -c——防单引号逃逸注入）。
	if !resvTimeRE.MatchString(startTime) {
		return nil, fmt.Errorf("invalid reservation starttime (want YYYY-MM-DDTHH:MM)")
	}
	if nodes != "" && !resvNodesRE.MatchString(nodes) {
		return nil, fmt.Errorf("invalid reservation nodes")
	}
	if partition != "" && !resvPartRE.MatchString(partition) {
		return nil, fmt.Errorf("invalid reservation partition")
	}
	if users != "" && !resvUsersRE.MatchString(users) {
		return nil, fmt.Errorf("invalid reservation users")
	}
	// scontrol 要求 nodes= / nodecnt= / corecnt= 至少其一：未指定时默认 nodecnt=1。
	spec := []string{"reservationname=" + name, "starttime=" + startTime,
		fmt.Sprintf("duration=%d", durationMin)}
	if nodes != "" {
		spec = append(spec, "nodes="+nodes)
	} else {
		spec = append(spec, "nodecnt=1")
	}
	if partition != "" {
		spec = append(spec, "partition="+partition)
	}
	if users != "" {
		spec = append(spec, "users="+users)
	}
	quoted := make([]string, len(spec))
	for i, kv := range spec {
		quoted[i] = "'" + kv + "'"
	}
	// 经 sh -c 2>&1：scontrol 的报错走 stderr，docker exec 默认丢弃——包进来给调用方。
	if out, err := s.runCluster("sh", "-c", "scontrol create reservation "+strings.Join(quoted, " ")+" 2>&1"); err != nil || strings.Contains(strings.ToUpper(string(out)), "ERROR") {
		return nil, fmt.Errorf("scontrol create reservation: %s", strings.TrimSpace(string(out)))
	}
	s.clusterAudit(ctx, actor, "reservations.create", "reservation:"+name, rid,
		fmt.Sprintf(`{"duration":%d,"users":%q}`, durationMin, users))
	res, _ := s.findReservation(ctx, name)
	return res, nil
}

// DeleteReservation scontrol delete reservation <name>（v3-X1：成功后落审计）。
func (s *Service) DeleteReservation(ctx context.Context, actor, name, rid string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid reservation name")
	}
	if out, err := s.runCluster("sh", "-c", "scontrol delete reservation="+name+" 2>&1"); err != nil || strings.Contains(strings.ToUpper(string(out)), "ERROR") {
		return fmt.Errorf("scontrol delete reservation: %s", strings.TrimSpace(string(out)))
	}
	s.clusterAudit(ctx, actor, "reservations.delete", "reservation:"+name, rid, "{}")
	return nil
}

// clusterAudit 落集群管理面审计（预约/QOS/分区共用；st nil=yaml 模式静默跳过）。
func (s *Service) clusterAudit(ctx context.Context, actor, action, target, rid, detail string) {
	if s.st == nil {
		return
	}
	_ = s.st.WriteAudit(ctx, actor, action, target, rid, detail)
}

// --- 租户配额可见性（v4-W3）：读数走 Slurm 权威，不信 DB 限额快照 ---
// （限额可能被集群侧直接改过——UpdateTenant 的 DB 字段只是设置时的记录。）

// TenantQuota 是一个租户的 GrpTRES 上限（原始串直出，前端解析 cpu=/mem=/gres/gpu=）。
type TenantQuota struct {
	TenantSlug    string `json:"tenantSlug"`
	ParentAccount string `json:"parentAccount"`
	GrpTRES       string `json:"grpTres"` // 空串=未设限（集群默认）
}

// ListTenantQuotas sacctmgr -nP show account format=name,grptres 单次全量 →
// 按租户父账号关联。需要用户库（租户清单）——yaml 模式 ensure() 503。
func (s *Service) ListTenantQuotas(ctx context.Context) ([]TenantQuota, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	out, err := s.runCluster("sh", "-c",
		`sacctmgr -nP show account format=name,grptres || true`)
	if err != nil {
		return nil, fmt.Errorf("sacctmgr show account: %w", err)
	}
	limits := map[string]string{}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(ln, "|", 2)
		if len(f) == 2 && f[0] != "" {
			if _, dup := limits[f[0]]; !dup { // 重复行取首条
				limits[f[0]] = f[1]
			}
		}
	}
	ts, err := s.st.Tenants(ctx)
	if err != nil {
		return nil, err
	}
	quotas := []TenantQuota{}
	for _, t := range ts {
		quotas = append(quotas, TenantQuota{
			TenantSlug: t.Slug, ParentAccount: t.ParentAccount, GrpTRES: limits[t.ParentAccount],
		})
	}
	return quotas, nil
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

// ParseQOSList 解析 sacctmgr show qos 输出，支持表头自适应及多版本无表头降级回退。
func ParseQOSList(out string) []QOS {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return []QOS{}
	}

	qosList := make([]QOS, 0, len(lines))

	firstLine := strings.TrimSpace(lines[0])
	hasHeader := false
	colMap := map[string]int{}
	if strings.Contains(strings.ToLower(firstLine), "name|") || strings.HasPrefix(strings.ToLower(firstLine), "name") {
		hasHeader = true
		for idx, col := range strings.Split(firstLine, "|") {
			colMap[strings.ToLower(strings.TrimSpace(col))] = idx
		}
		lines = lines[1:]
	}

	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if !strings.Contains(ln, "|") {
			continue
		}
		f := strings.Split(ln, "|")
		if len(f) < 1 || strings.TrimSpace(f[0]) == "" {
			continue
		}

		get := func(i int) string {
			if i >= 0 && i < len(f) {
				return strings.TrimSpace(f[i])
			}
			return ""
		}

		var q QOS
		if hasHeader {
			getByHeader := func(keys ...string) string {
				for _, k := range keys {
					if idx, ok := colMap[k]; ok {
						if v := get(idx); v != "" {
							return v
						}
					}
				}
				return ""
			}
			q.Name = getByHeader("name")
			q.Priority = getByHeader("priority")
			q.GrpTRES = getByHeader("grptres")
			q.MaxTRES = getByHeader("maxtres", "maxtrespj", "maxtresperjob")
			q.MaxTRESPerUser = getByHeader("maxtrespu", "maxtresperuser")
			if q.MaxTRES == "" && q.MaxTRESPerUser != "" {
				q.MaxTRES = q.MaxTRESPerUser
			}
			if q.MaxTRESPerUser == "" && q.MaxTRES != "" {
				q.MaxTRESPerUser = q.MaxTRES
			}
			q.MaxJobs = getByHeader("grpjobs", "maxjobs", "grpj")
			q.MaxJobsPerUser = getByHeader("maxjobspu", "maxjobsperuser")
			if q.MaxJobs == "" && q.MaxJobsPerUser != "" {
				q.MaxJobs = q.MaxJobsPerUser
			}
			if q.MaxJobsPerUser == "" && q.MaxJobs != "" {
				q.MaxJobsPerUser = q.MaxJobs
			}
			q.MaxSubmitJobsPerUser = getByHeader("maxsubmitpu", "maxsubmitjobspu", "maxsubmitjobsperuser")
			q.MaxWall = getByHeader("maxwall", "maxwallpj", "maxwallduration", "maxwallpu")
			q.MaxWallDuration = q.MaxWall
			q.Description = getByHeader("description", "descr")
		} else {
			q.Name = get(0)
			q.Priority = get(1)
			q.GrpTRES = get(2)

			if len(f) >= 7 && (qosValueREs["MaxWallDuration"].MatchString(get(4)) || get(4) == "" && (get(5) != "" || get(6) != "")) {
				// 7/8 列格式: name, priority, grptres, maxtrespu, maxwall, maxjobspu, maxsubmitjobspu, description
				q.MaxTRESPerUser = get(3)
				q.MaxTRES = get(3)
				q.MaxWall = get(4)
				q.MaxWallDuration = get(4)
				q.MaxJobsPerUser = get(5)
				q.MaxJobs = get(5)
				q.MaxSubmitJobsPerUser = get(6)
				if len(f) >= 8 {
					q.Description = get(7)
				}
			} else if len(f) >= 9 {
				// 9 列格式: name, priority, grptres, maxtrespu, maxtres, maxjobspu, maxsubmitpu, maxwall, description
				q.MaxTRESPerUser = get(3)
				q.MaxTRES = get(4)
				if q.MaxTRES == "" {
					q.MaxTRES = q.MaxTRESPerUser
				}
				q.MaxJobsPerUser = get(5)
				q.MaxJobs = q.MaxJobsPerUser
				q.MaxSubmitJobsPerUser = get(6)
				q.MaxWall = get(7)
				q.MaxWallDuration = q.MaxWall
				q.Description = get(8)
			} else if len(f) >= 6 {
				// 6 列降级格式: name, priority, grptres, maxtres, maxwall, grpj
				q.MaxTRES = get(3)
				q.MaxTRESPerUser = get(3)
				q.MaxWall = get(4)
				q.MaxWallDuration = get(4)
				q.MaxJobs = get(5)
				q.MaxJobsPerUser = get(5)
			} else if len(f) >= 2 {
				// Partial
				if len(f) > 2 {
					q.GrpTRES = get(2)
				}
			}
		}

		if q.Name != "" {
			qosList = append(qosList, q)
		}
	}

	return qosList
}

// ListQOS sacctmgr -nP show qos 全量查询并解析。
func (s *Service) ListQOS(ctx context.Context) ([]QOS, error) {
	out, err := s.runCluster("sh", "-c",
		`sacctmgr -nP show qos format=name,priority,grptres,maxtrespu,maxwall,maxjobspu,maxsubmitjobspu,description || `+
			`sacctmgr -nP show qos format=name,priority,grptres,maxtresperuser,maxwall,maxjobsperuser,maxsubmitjobsperuser,description || `+
			`sacctmgr -nP show qos format=name,priority,grptres,maxtres,maxwall,grpj`)
	if err != nil {
		return nil, fmt.Errorf("sacctmgr show qos: %w", err)
	}
	return ParseQOSList(string(out)), nil
}

// GetQOS 获取指定名称的 QOS 详情。
func (s *Service) GetQOS(ctx context.Context, name string) (*QOS, error) {
	if !qosNameRE.MatchString(name) {
		return nil, fmt.Errorf("invalid qos name")
	}
	qosList, err := s.ListQOS(ctx)
	if err != nil {
		return nil, err
	}
	for i := range qosList {
		if qosList[i].Name == name {
			return &qosList[i], nil
		}
	}
	return nil, ErrQOSNotFound
}

// CreateQOS 创建 Slurm QOS 并设置各项限制参数。成功后落审计。
func (s *Service) CreateQOS(ctx context.Context, actor, name string, u QOSUpdates, rid string) (*QOS, error) {
	if !qosNameRE.MatchString(name) {
		return nil, fmt.Errorf("invalid qos name")
	}
	if err := ValidateQOSFields(u); err != nil {
		return nil, err
	}

	spec := []string{}
	for _, f := range qosFields {
		if v := strings.TrimSpace(f.Get(u)); v != "" {
			spec = append(spec, f.Key+"="+v)
		}
	}
	quoted := make([]string, len(spec))
	for i, kv := range spec {
		quoted[i] = "'" + kv + "'"
	}

	cmd := "sacctmgr -i add qos " + name
	if len(quoted) > 0 {
		cmd += " " + strings.Join(quoted, " ")
	}
	cmd += " 2>&1"

	out, err := s.runCluster("sh", "-c", cmd)
	if err != nil {
		return nil, fmt.Errorf("sacctmgr add qos: %w", err)
	}
	outStr := strings.TrimSpace(string(out))
	if strings.Contains(strings.ToUpper(outStr), "ERROR") {
		return nil, fmt.Errorf("sacctmgr add qos: %s", outStr)
	}

	detailBytes, _ := json.Marshal(u)
	s.clusterAudit(ctx, actor, "qos.create", "qos:"+name, rid, string(detailBytes))

	if q, err := s.GetQOS(ctx, name); err == nil && q != nil {
		return q, nil
	}
	return &QOS{
		Name:                 name,
		Priority:             u.Priority,
		GrpTRES:              u.GrpTRES,
		MaxTRES:              u.MaxTRES,
		MaxTRESPerUser:       u.MaxTRESPerUser,
		MaxJobs:              u.MaxJobs,
		MaxJobsPerUser:       u.MaxJobsPerUser,
		MaxSubmitJobsPerUser: u.MaxSubmitJobsPerUser,
		MaxWall:              u.MaxWall,
		MaxWallDuration:      u.MaxWallDuration,
		Description:          u.Description,
	}, nil
}

// UpdateQOS 修改已有 QOS 限额配置（sacctmgr -i modify qos <name> set K=V...）。成功后落审计。
func (s *Service) UpdateQOS(ctx context.Context, actor, name string, u QOSUpdates, rid string) error {
	if !qosNameRE.MatchString(name) {
		return fmt.Errorf("invalid qos name")
	}
	if err := ValidateQOSUpdates(u); err != nil {
		return err
	}

	spec := []string{}
	for _, f := range qosFields {
		if v := strings.TrimSpace(f.Get(u)); v != "" {
			spec = append(spec, f.Key+"="+v)
		}
	}
	quoted := make([]string, len(spec))
	for i, kv := range spec {
		quoted[i] = "'" + kv + "'"
	}

	cmd := "sacctmgr -i modify qos " + name + " set " + strings.Join(quoted, " ") + " 2>&1"
	out, err := s.runCluster("sh", "-c", cmd)
	if err != nil {
		return fmt.Errorf("sacctmgr modify qos: %w", err)
	}
	outStr := strings.TrimSpace(string(out))
	upper := strings.ToUpper(outStr)
	if strings.Contains(upper, "UNKNOWN QOS") || strings.Contains(upper, "NOTHING MODIFIED") || strings.Contains(upper, "NOTHING DELETED") || strings.Contains(upper, "UNKNOWN") {
		return ErrQOSNotFound
	}
	if strings.Contains(upper, "ERROR") {
		return fmt.Errorf("sacctmgr modify qos: %s", outStr)
	}

	detailBytes, _ := json.Marshal(u)
	s.clusterAudit(ctx, actor, "qos.modify", "qos:"+name, rid, string(detailBytes))
	return nil
}

// DeleteQOS 删除指定 QOS（sacctmgr -i delete qos <name>）。成功后落审计。
func (s *Service) DeleteQOS(ctx context.Context, actor, name, rid string) error {
	if !qosNameRE.MatchString(name) {
		return fmt.Errorf("invalid qos name")
	}
	if strings.EqualFold(name, "normal") {
		return fmt.Errorf("cannot delete default normal qos")
	}

	cmd := "sacctmgr -i delete qos " + name + " 2>&1"
	out, err := s.runCluster("sh", "-c", cmd)
	if err != nil {
		return fmt.Errorf("sacctmgr delete qos: %w", err)
	}
	outStr := strings.TrimSpace(string(out))
	upper := strings.ToUpper(outStr)
	if strings.Contains(upper, "UNKNOWN QOS") || strings.Contains(upper, "NOTHING DELETED") || strings.Contains(upper, "NOTHING MODIFIED") || strings.Contains(upper, "UNKNOWN") {
		return ErrQOSNotFound
	}
	if strings.Contains(upper, "ERROR") {
		return fmt.Errorf("sacctmgr delete qos: %s", outStr)
	}

	s.clusterAudit(ctx, actor, "qos.delete", "qos:"+name, rid, "{}")
	return nil
}

// SetTenantQOS 把 QOS 绑到租户父账号（sacctmgr modify account <parent> set qos=...）。
// 若 qosName 为空字符串，则清除租户的 QOS 绑定（set qos=）。
// v3-X1：成功后落审计。
func (s *Service) SetTenantQOS(ctx context.Context, actor, tenantSlug, qosName, rid string) error {
	// 非空时才做格式校验，空字符串表示"清除绑定"
	if qosName != "" && !nameRE.MatchString(qosName) {
		return fmt.Errorf("invalid qos name")
	}
	t, err := s.st.TenantBySlug(ctx, tenantSlug)
	if err != nil {
		return err
	}
	if _, err := s.runCluster("sacctmgr", "-i", "modify", "account", t.ParentAccount, "set", "qos="+qosName); err != nil {
		return fmt.Errorf("sacctmgr modify account qos: %w", err)
	}
	action := qosName
	if action == "" {
		action = "<cleared>"
	}
	s.clusterAudit(ctx, actor, "tenant.qos", "tenant:"+tenantSlug, rid, `{"qos":`+strconv.Quote(action)+`}`)
	return nil
}

// GetTenantQOS 查询租户父账号当前绑定的默认 QOS（sacctmgr show account <parent>）。
// 返回 defaultQOS 与允许的 qos 清单；若未绑定则均为空。
func (s *Service) GetTenantQOS(ctx context.Context, tenantSlug string) (defaultQOS string, allowedQOS []string, err error) {
	t, err := s.st.TenantBySlug(ctx, tenantSlug)
	if err != nil {
		return "", nil, err
	}
	out, err := s.runCluster("sacctmgr", "-nP", "show", "account", t.ParentAccount,
		"format=defaultqos,qos")
	if err != nil {
		return "", nil, fmt.Errorf("sacctmgr show account: %w", err)
	}
	// 输出格式：defaultqos|qos\n  e.g. "normal|normal,vip\n"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) < 2 {
			continue
		}
		defaultQOS = strings.TrimSpace(parts[0])
		qosStr := strings.TrimSpace(parts[1])
		if qosStr != "" {
			for _, q := range strings.Split(qosStr, ",") {
				q = strings.TrimSpace(q)
				if q != "" {
					allowedQOS = append(allowedQOS, q)
				}
			}
		}
		break
	}
	return defaultQOS, allowedQOS, nil
}


// SetUserQOS 设置指定用户的 Slurm 关联 QOS（默认 QOS 与允许使用的 QOS 清单）。
// actor: 操作人用户名
// username: 目标平台用户名
// tenantSlug: 调用者租户 Slug（租户管理员调用时非空；平台管理员调用时若为空则自动从用户库解析）
// req: 包含 DefaultQOS 与 AllowedQOS
// rid: 请求链路 Request ID
func (s *Service) SetUserQOS(ctx context.Context, actor, username, tenantSlug string, req UserQOSUpdates, rid string) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if !unixSafeRE.MatchString(username) {
		return fmt.Errorf("invalid username %q", username)
	}
	if tenantSlug != "" && !unixSafeRE.MatchString(tenantSlug) {
		return fmt.Errorf("invalid tenant slug %q", tenantSlug)
	}
	if err := ValidateUserQOSUpdates(&req); err != nil {
		return err
	}

	var targetUser *auth.User
	var targetTenant *store.Tenant

	if tenantSlug != "" {
		// 租户作用域调用（租户管理员）
		t, err := s.st.TenantBySlug(ctx, tenantSlug)
		if err != nil {
			return err
		}
		targetTenant = t

		users, err := s.st.ListTenantUsers(ctx, tenantSlug)
		if err != nil {
			return err
		}
		for _, u := range users {
			if u.Username == username {
				targetUser = &u
				break
			}
		}
		if targetUser == nil {
			return store.ErrNotFound
		}
		// 防提权与关键系统管理员保护
		if err := s.protectBuiltinAdmin(ctx, actor, username); err != nil {
			return err
		}
	} else {
		// 平台管理员调用
		u, ok := s.st.Lookup(username)
		if !ok {
			return store.ErrNotFound
		}
		targetUser = u

		ts := targetUser.TenantSlug
		if ts == "" {
			ts = targetUser.OrgSlug
		}
		t, err := s.st.TenantBySlug(ctx, ts)
		if err != nil {
			return err
		}
		targetTenant = t
		tenantSlug = targetTenant.Slug
	}

	clusterUser := targetUser.ClusterUser
	if clusterUser == "" {
		clusterUser = targetUser.Username
	}
	parentAccount := targetTenant.ParentAccount
	if parentAccount == "" {
		parentAccount = targetTenant.Slug
	}

	// 构造 sacctmgr 命令
	var setClauses []string
	if len(req.AllowedQOS) > 0 {
		setClauses = append(setClauses, "qos="+strings.Join(req.AllowedQOS, ","))
	}
	if req.DefaultQOS != "" {
		setClauses = append(setClauses, "defaultqos="+req.DefaultQOS)
	}

	cmd := fmt.Sprintf("sacctmgr -i modify user %s account=%s set %s 2>&1", clusterUser, parentAccount, strings.Join(setClauses, " "))
	out, err := s.runCluster("sh", "-c", cmd)
	if err != nil {
		return fmt.Errorf("sacctmgr modify user qos: %w", err)
	}
	outStr := strings.TrimSpace(string(out))
	upper := strings.ToUpper(outStr)
	if strings.Contains(upper, "UNKNOWN QOS") {
		return ErrQOSNotFound
	}
	if strings.Contains(upper, "UNKNOWN USER") {
		return store.ErrNotFound
	}
	if strings.Contains(upper, "ERROR") {
		return fmt.Errorf("sacctmgr modify user qos: %s", outStr)
	}

	// 刷新 slurmctld association 缓存
	_, _ = s.runCluster("scontrol", "reconfigure")

	detailMap := map[string]any{
		"tenant":     tenantSlug,
		"defaultQOS": req.DefaultQOS,
		"defaultQos": req.DefaultQOS,
		"allowedQOS": req.AllowedQOS,
		"allowedQos": req.AllowedQOS,
	}
	detailBytes, _ := json.Marshal(detailMap)
	s.clusterAudit(ctx, actor, "qos.user.set", "user:"+username, rid, string(detailBytes))
	return nil
}

// SetTenantUserQOS 租户管理员修改本租户成员 QOS。
func (s *Service) SetTenantUserQOS(ctx context.Context, actor, tenantSlug, username string, req UserQOSUpdates, rid string) error {
	return s.SetUserQOS(ctx, actor, username, tenantSlug, req, rid)
}

// ParseUserQOS 解析 sacctmgr show assoc 输出。
func ParseUserQOS(out string, clusterUser, parentAccount string) *UserQOSInfo {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	colMap := map[string]int{}
	hasHeader := false

	if len(lines) > 0 {
		firstLine := strings.TrimSpace(lines[0])
		if strings.Contains(strings.ToLower(firstLine), "user|") || strings.HasPrefix(strings.ToLower(firstLine), "user") {
			hasHeader = true
			for idx, col := range strings.Split(firstLine, "|") {
				colMap[strings.ToLower(strings.TrimSpace(col))] = idx
			}
			lines = lines[1:]
		}
	}

	var bestRow []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || !strings.Contains(ln, "|") {
			continue
		}
		f := strings.Split(ln, "|")
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		if len(f) < 2 {
			continue
		}

		userVal := f[0]
		acctVal := f[1]
		if hasHeader {
			if uIdx, ok := colMap["user"]; ok && uIdx < len(f) {
				userVal = f[uIdx]
			}
			if aIdx, ok := colMap["account"]; ok && aIdx < len(f) {
				acctVal = f[aIdx]
			}
		}

		if clusterUser != "" && !strings.EqualFold(userVal, clusterUser) {
			continue
		}

		if parentAccount != "" && strings.EqualFold(acctVal, parentAccount) {
			bestRow = f
			break
		}
		if bestRow == nil {
			bestRow = f
		}
	}

	info := &UserQOSInfo{
		ClusterUser: clusterUser,
		Account:     parentAccount,
		DefaultQOS:  "normal",
		AllowedQOS:  []string{"normal"},
	}

	if bestRow != nil {
		var qosRaw, defQosRaw, acctRaw, uRaw string
		if hasHeader {
			if idx, ok := colMap["qos"]; ok && idx < len(bestRow) {
				qosRaw = bestRow[idx]
			}
			if idx, ok := colMap["defqos"]; ok && idx < len(bestRow) {
				defQosRaw = bestRow[idx]
			}
			if idx, ok := colMap["account"]; ok && idx < len(bestRow) {
				acctRaw = bestRow[idx]
			}
			if idx, ok := colMap["user"]; ok && idx < len(bestRow) {
				uRaw = bestRow[idx]
			}
		} else {
			if len(bestRow) > 0 {
				uRaw = bestRow[0]
			}
			if len(bestRow) > 1 {
				acctRaw = bestRow[1]
			}
			if len(bestRow) > 2 {
				qosRaw = bestRow[2]
			}
			if len(bestRow) > 3 {
				defQosRaw = bestRow[3]
			}
		}

		if uRaw != "" {
			info.ClusterUser = uRaw
		}
		if acctRaw != "" {
			info.Account = acctRaw
		}

		var allowed []string
		seen := map[string]bool{}
		for _, q := range strings.FieldsFunc(qosRaw, func(r rune) bool {
			return r == ',' || r == '+' || r == ' ' || r == '\t'
		}) {
			q = strings.TrimSpace(q)
			if q != "" && !seen[q] {
				seen[q] = true
				allowed = append(allowed, q)
			}
		}

		defQos := strings.TrimSpace(defQosRaw)
		if len(allowed) > 0 {
			info.AllowedQOS = allowed
		}
		if defQos != "" {
			info.DefaultQOS = defQos
		} else if len(info.AllowedQOS) > 0 {
			if seen["normal"] {
				info.DefaultQOS = "normal"
			} else {
				info.DefaultQOS = info.AllowedQOS[0]
			}
		}
	}

	return info
}

// GetUserQOS 查询指定用户的 Slurm 关联 QOS 详情。
func (s *Service) GetUserQOS(ctx context.Context, username, tenantSlug string) (*UserQOSInfo, error) {
	if err := s.ensure(); err != nil {
		return nil, err
	}
	if !unixSafeRE.MatchString(username) {
		return nil, fmt.Errorf("invalid username %q", username)
	}
	if tenantSlug != "" && !unixSafeRE.MatchString(tenantSlug) {
		return nil, fmt.Errorf("invalid tenant slug %q", tenantSlug)
	}

	var targetUser *auth.User
	var targetTenant *store.Tenant

	if tenantSlug != "" {
		t, err := s.st.TenantBySlug(ctx, tenantSlug)
		if err != nil {
			return nil, err
		}
		targetTenant = t

		users, err := s.st.ListTenantUsers(ctx, tenantSlug)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if u.Username == username {
				targetUser = &u
				break
			}
		}
		if targetUser == nil {
			return nil, store.ErrNotFound
		}
	} else {
		u, ok := s.st.Lookup(username)
		if !ok {
			return nil, store.ErrNotFound
		}
		targetUser = u

		ts := targetUser.TenantSlug
		if ts == "" {
			ts = targetUser.OrgSlug
		}
		t, err := s.st.TenantBySlug(ctx, ts)
		if err != nil {
			return nil, err
		}
		targetTenant = t
		tenantSlug = targetTenant.Slug
	}

	clusterUser := targetUser.ClusterUser
	if clusterUser == "" {
		clusterUser = targetUser.Username
	}
	parentAccount := targetTenant.ParentAccount
	if parentAccount == "" {
		parentAccount = targetTenant.Slug
	}

	cmd := fmt.Sprintf("sacctmgr -nP show assoc where user=%s format=User,Account,QOS,DefQOS", clusterUser)
	out, err := s.runCluster("sh", "-c", cmd)
	if err != nil {
		return nil, fmt.Errorf("sacctmgr show assoc: %w", err)
	}

	info := ParseUserQOS(string(out), clusterUser, parentAccount)
	info.Username = username
	info.TenantSlug = tenantSlug
	return info, nil
}

// GetAvailableQOS 查询用户可用的 QOS 策略对象清单与默认 QOS。
func (s *Service) GetAvailableQOS(ctx context.Context, username, tenantSlug string) (*AvailableQOSResponse, error) {
	userQOS, err := s.GetUserQOS(ctx, username, tenantSlug)
	if err != nil {
		return nil, err
	}

	allQOS, err := s.ListQOS(ctx)
	if err != nil {
		return nil, err
	}

	qosMap := make(map[string]QOS)
	for _, q := range allQOS {
		qosMap[strings.ToLower(q.Name)] = q
	}

	allowedObjects := make([]QOS, 0, len(userQOS.AllowedQOS))
	for _, name := range userQOS.AllowedQOS {
		if q, ok := qosMap[strings.ToLower(name)]; ok {
			allowedObjects = append(allowedObjects, q)
		} else {
			allowedObjects = append(allowedObjects, QOS{Name: name})
		}
	}

	if len(allowedObjects) == 0 {
		if q, ok := qosMap["normal"]; ok {
			allowedObjects = append(allowedObjects, q)
		} else {
			allowedObjects = append(allowedObjects, QOS{
				Name:        "normal",
				Priority:    "0",
				Description: "Standard default QOS",
			})
		}
	}

	defaultQos := userQOS.DefaultQOS
	if defaultQos == "" {
		if len(allowedObjects) > 0 {
			defaultQos = allowedObjects[0].Name
		} else {
			defaultQos = "normal"
		}
	}

	return &AvailableQOSResponse{
		DefaultQOS:   defaultQos,
		AllowedQOS:   allowedObjects,
		AvailableQOS: allowedObjects,
	}, nil
}

// runCluster 执行集群管理命令（默认 slurmctld CLI；测试可换 Service.runner——nil 即默认）。
func (s *Service) runCluster(args ...string) ([]byte, error) {
	r := s.runner
	if r == nil {
		r = defaultClusterRunner
	}
	return r(args...)
}

// --- 分区管理（v2 增量）：scontrol show/update partition 直通（partitions:manage）。
// 与预约/QOS 同教义：21.08 slurmrestd 无分区写端点，CLI 是唯一通路；编辑弹层的
// 当前值同样取自 CLI（slurmrestd 分区视图只富化了 5 个字段，嵌套 schema 不可考）。

// PartitionDetail 是 scontrol show partition <name> 的解析视图（编辑弹层当前值来源）。
type PartitionDetail struct {
	Name          string `json:"name"`
	State         string `json:"state"`
	Default       string `json:"default"`
	MaxTime       string `json:"maxTime"`
	DefMemPerCPU  string `json:"defMemPerCPU"`
	Nodes         string `json:"nodes"`
	OverSubscribe string `json:"overSubscribe"`
	AllowAccounts string `json:"allowAccounts"`
	AllowGroups   string `json:"allowGroups"`
}

// PartitionUpdates 是分区可修改字段白名单（空串=不变更；值合法性由 handler 层
// partitionValueREs 校验——同 UpdateTenant 的 limitRE 前置教义）。
type PartitionUpdates struct {
	State         string `json:"state"`
	Default       string `json:"default"`
	MaxTime       string `json:"maxTime"`
	DefMemPerCPU  string `json:"defMemPerCPU"`
	Nodes         string `json:"nodes"`
	OverSubscribe string `json:"overSubscribe"`
	AllowAccounts string `json:"allowAccounts"`
	AllowGroups   string `json:"allowGroups"`
}

// partitionValueREs 逐字段值白名单（防注入 scontrol）：
//   - State/Default/OverSubscribe：Slurm 枚举（OverSubscribe 含 FORCE:n 计数形式）
//   - MaxTime：UNLIMITED 或 分钟[[:ss]][-[dd-]hh:mm[:ss]] 复合时限字符集
//   - DefMemPerCPU：UNLIMITED 或 裸数/带 K/M/G/T 后缀
//   - Nodes：hostlist 表达式（c1,c2[3-5]）
//   - AllowAccounts/AllowGroups：逗号清单
var partitionValueREs = map[string]*regexp.Regexp{
	"State":         regexp.MustCompile(`(?i)^(UP|DOWN|DRAIN|INACTIVE)$`),
	"Default":       regexp.MustCompile(`(?i)^(YES|NO)$`),
	"MaxTime":       regexp.MustCompile(`(?i)^(UNLIMITED|[0-9][0-9:?-]{0,31})$`),
	"DefMemPerCPU":  regexp.MustCompile(`(?i)^(UNLIMITED|[0-9]+[KMGT]?)$`),
	"OverSubscribe": regexp.MustCompile(`(?i)^(YES|NO|EXCLUSIVE|FORCE(:[0-9]+)?)$`),
	"Nodes":         regexp.MustCompile(`^[0-9A-Za-z,\[\]-]{1,512}$`),
	"AllowAccounts": regexp.MustCompile(`^[0-9A-Za-z,_-]{1,256}$`),
	"AllowGroups":   regexp.MustCompile(`^[0-9A-Za-z,_-]{1,256}$`),
}

// partitionFields 是 PartitionUpdates 字段 → scontrol 键的有序映射（构建命令与校验共用）。
var partitionFields = []struct {
	Key string // scontrol 键（= 结构体字段名）
	Get func(PartitionUpdates) string
}{
	{"State", func(u PartitionUpdates) string { return u.State }},
	{"Default", func(u PartitionUpdates) string { return u.Default }},
	{"MaxTime", func(u PartitionUpdates) string { return u.MaxTime }},
	{"DefMemPerCPU", func(u PartitionUpdates) string { return u.DefMemPerCPU }},
	{"OverSubscribe", func(u PartitionUpdates) string { return u.OverSubscribe }},
	{"Nodes", func(u PartitionUpdates) string { return u.Nodes }},
	{"AllowAccounts", func(u PartitionUpdates) string { return u.AllowAccounts }},
	{"AllowGroups", func(u PartitionUpdates) string { return u.AllowGroups }},
}

// ValidatePartitionUpdates 字段白名单校验（handler 前置调用，非法值 → 400 文案）。
// 至少一个非空字段，否则拒绝（防空 PATCH 直通 scontrol 报晦涩错）。
func ValidatePartitionUpdates(u PartitionUpdates) error {
	set := 0
	for _, f := range partitionFields {
		v := f.Get(u)
		if v == "" {
			continue
		}
		set++
		if !partitionValueREs[f.Key].MatchString(v) {
			return fmt.Errorf("invalid %s value %q", f.Key, v)
		}
	}
	if set == 0 {
		return fmt.Errorf("no partition fields to update")
	}
	return nil
}

// GetPartition scontrol show partition <name>（编辑弹层当前值）。
func (s *Service) GetPartition(ctx context.Context, name string) (*PartitionDetail, error) {
	if !nameRE.MatchString(name) {
		return nil, fmt.Errorf("invalid partition name")
	}
	out, err := s.runCluster("scontrol", "show", "partition", name)
	if err != nil {
		return nil, fmt.Errorf("scontrol show partition: %w", err)
	}
	d := parsePartitionDetail(string(out))
	if d == nil {
		return nil, ErrPartitionNotFound
	}
	return d, nil
}

// UpdatePartition scontrol update partition=<name> K=V...（值白名单在 handler 校验，
// 这里只复查名字与字段集）。成功后写 audit_log（st 为 nil 的 yaml 模式跳过——与
// 预约/QOS 一致，纯集群操作不依赖用户库）。
func (s *Service) UpdatePartition(ctx context.Context, actor, name string, u PartitionUpdates, rid string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid partition name")
	}
	if err := ValidatePartitionUpdates(u); err != nil {
		return err
	}
	spec := []string{"partition=" + name}
	for _, f := range partitionFields {
		if v := f.Get(u); v != "" {
			spec = append(spec, f.Key+"="+v)
		}
	}
	quoted := make([]string, len(spec))
	for i, kv := range spec {
		quoted[i] = "'" + kv + "'"
	}
	out, err := s.runCluster("sh", "-c", "scontrol update "+strings.Join(quoted, " ")+" 2>&1")
	if err != nil {
		return fmt.Errorf("scontrol update partition: %w", err)
	}
	if strings.Contains(strings.ToUpper(string(out)), "ERROR") {
		return fmt.Errorf("scontrol update partition: %s", strings.TrimSpace(string(out)))
	}
	if s.st != nil {
		if detail, err := json.Marshal(u); err == nil {
			s.clusterAudit(ctx, actor, "partition.update", "partition:"+name, rid, string(detail))
		}
	}
	return nil
}

// parsePartitionDetail 解析 scontrol show partition 输出（与 parseReservations 同手法：
// 逐行 Fields 按 "=" 切 kv；输出无 PartitionName= → nil）。
func parsePartitionDetail(out string) *PartitionDetail {
	if !strings.Contains(out, "PartitionName=") {
		return nil
	}
	kv := map[string]string{}
	for _, ln := range strings.Split(out, "\n") {
		for _, f := range strings.Fields(ln) {
			if i := strings.Index(f, "="); i > 0 {
				kv[f[:i]] = f[i+1:]
			}
		}
	}
	if kv["PartitionName"] == "" {
		return nil
	}
	return &PartitionDetail{
		Name:          kv["PartitionName"],
		State:         kv["State"],
		Default:       kv["Default"],
		MaxTime:       kv["MaxTime"],
		DefMemPerCPU:  kv["DefMemPerCPU"],
		Nodes:         kv["Nodes"],
		OverSubscribe: kv["OverSubscribe"],
		AllowAccounts: kv["AllowAccounts"],
		AllowGroups:   kv["AllowGroups"],
	}
}
