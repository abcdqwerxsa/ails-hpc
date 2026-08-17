package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func newDetailSvc(sacctOut string, tail string) *jobServiceImpl {
	return &jobServiceImpl{
		localJobs: map[int]*JobSummary{},
		sacctRun: func(args ...string) ([]byte, error) {
			return []byte(sacctOut), nil
		},
		tailOut: func(jobID int) (string, error) { return tail, nil },
	}
}

// TestJobDetail_ParsesMainRecord：-P 首条主记录解析，步骤行跳过，tail 拼接。
func TestJobDetail_ParsesMainRecord(t *testing.T) {
	out := "66|myjob|ailsmember|ailsmember|standard|COMPLETED|63|0:0|2026-08-17T06:07:41|2026-08-17T06:07:44|2026-08-17T06:07:40\n" +
		"66.batch|batch||ailsmember||COMPLETED|63|0:0|s|e|sub\n"
	s := newDetailSvc(out, "OUT-1\n")
	d, err := s.JobDetail(context.Background(), 66)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "myjob" || d.Owner != "ailsmember" || d.State != "COMPLETED" ||
		d.ElapsedSec != 63 || d.ExitCode != "0:0" || d.Partition != "standard" {
		t.Errorf("detail = %+v", d)
	}
	if !strings.Contains(d.StdoutTail, "OUT-1") {
		t.Errorf("stdout tail = %q", d.StdoutTail)
	}
}

// TestJobDetail_NotFound：sacct 无记录 → ErrJobNotFound。
func TestJobDetail_NotFound(t *testing.T) {
	s := newDetailSvc("", "")
	if _, err := s.JobDetail(context.Background(), 999); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("want ErrJobNotFound, got %v", err)
	}
}

func TestHistory_ParsesFiltersDesc(t *testing.T) {
	out := "70|older|ailsmember|ailsmember|standard|COMPLETED|10|0:0|s1|st1|e1\n" +
		"70.batch|batch||ailsmember||COMPLETED|10|0:0|s|st|e\n" +
		"71|newer|ailsmember|ailsmember|performance|FAILED|5|1:0|s2|st2|e2\n" +
		"72|other|ailsother|ailsother|standard|COMPLETED|3|0:0|s3|st3|e3\n"
	s := &jobServiceImpl{
		localJobs: map[int]*JobSummary{},
		sacctRun:  func(args ...string) ([]byte, error) { return []byte(out), nil },
	}
	// 全量：倒序（71 在 70 前），步骤行忽略
	es, err := s.History(context.Background(), HistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 || es[0].JobID != 72 || es[1].JobID != 71 || es[2].JobID != 70 {
		t.Fatalf("order/rows = %+v", es)
	}
	if es[1].State != "FAILED" || es[1].ElapsedSec != 5 || es[1].ExitCode != "1:0" || es[1].Owner != "ailsmember" {
		t.Errorf("row71 = %+v", es[1])
	}
	// 按用户过滤
	es, _ = s.History(context.Background(), HistoryQuery{User: "ailsmember"})
	if len(es) != 2 {
		t.Fatalf("user filter = %d rows", len(es))
	}
	// 按状态过滤
	es, _ = s.History(context.Background(), HistoryQuery{State: "failed"})
	if len(es) != 1 || es[0].JobID != 71 {
		t.Fatalf("state filter = %+v", es)
	}
	// limit
	es, _ = s.History(context.Background(), HistoryQuery{Limit: 1})
	if len(es) != 1 {
		t.Fatalf("limit = %d", len(es))
	}
}
