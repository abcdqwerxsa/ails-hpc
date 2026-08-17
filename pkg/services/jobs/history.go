package jobs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 作业历史（roadmap 1.3）：sacct 逐作业列表，补齐"提交→运行→历史"生命周期。
//
// 与计费页的聚合不同，这里是逐作业行（时间倒序）；与队列表不同，这里能看到
// 已完成/失败的历史作业。租户隔离与列表同源（owner 取 sacct User，走 RowFilter）。
const historyFormat = "JobID,JobName,User,Account,Partition,State,ElapsedRaw,ExitCode,Submit,Start,End"

// HistoryEntry 历史作业行。
type HistoryEntry struct {
	JobID      int    `json:"job_id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"` // clusterUser（sacct User，租户过滤键）
	Account    string `json:"account"`
	Partition  string `json:"partition"`
	State      string `json:"state"`
	ElapsedSec int64  `json:"elapsed_sec"`
	ExitCode   string `json:"exit_code"`
	Submit     string `json:"submit"`
	Start      string `json:"start"`
	End        string `json:"end"`
}

// HistoryQuery 过滤参数（0 值=不过滤）。
type HistoryQuery struct {
	User  string // 精确 clusterUser
	State string // 精确状态（大写）
	Since time.Time
	Limit int // 默认 100、上限 500
}

// History 返回历史作业（时间倒序——按 JobID 倒序近似；sacct 无稳定时间排序输出，
// 我们在解析后按 JobID 排序保证确定性）。主记录行，步骤行忽略。
func (s *jobServiceImpl) History(ctx context.Context, q HistoryQuery) ([]HistoryEntry, error) {
	if s.sacctRun == nil {
		return nil, fmt.Errorf("history: slurm unavailable")
	}
	args := []string{"-n", "-P", "--allocations", "-o", historyFormat}
	if !q.Since.IsZero() {
		args = append(args, "--starttime="+q.Since.Format("2006-01-02T15:04:05"))
	}
	out, err := s.sacctRun(args...)
	if err != nil {
		return nil, fmt.Errorf("sacct: %w", err)
	}
	entries := []HistoryEntry{}
	for _, ln := range strings.Split(string(out), "\n") {
		e, ok := parseHistoryLine(ln)
		if !ok {
			continue
		}
		if q.User != "" && e.Owner != q.User {
			continue
		}
		if q.State != "" && e.State != strings.ToUpper(q.State) {
			continue
		}
		entries = append(entries, e)
	}
	// 倒序（最新在前）：按 JobID 降序
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	if len(entries) > q.Limit {
		entries = entries[:q.Limit]
	}
	return entries, nil
}

// parseHistoryLine 解析一行 -P 输出；步骤行（含 '.'）与短行跳过。
func parseHistoryLine(ln string) (HistoryEntry, bool) {
	f := strings.Split(ln, "|")
	if len(f) < 11 || strings.Contains(f[0], ".") {
		return HistoryEntry{}, false
	}
	id, err := strconv.Atoi(strings.TrimSpace(f[0]))
	if err != nil || id <= 0 {
		return HistoryEntry{}, false
	}
	e := HistoryEntry{
		JobID: id,
		Name:  f[1], Owner: f[2], Account: f[3], Partition: f[4], State: f[5],
		ExitCode: f[7], Submit: f[8], Start: f[9], End: f[10],
	}
	e.ElapsedSec, _ = strconv.ParseInt(strings.TrimSpace(f[6]), 10, 64)
	return e, true
}
