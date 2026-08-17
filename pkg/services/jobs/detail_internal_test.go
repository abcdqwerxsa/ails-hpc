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
