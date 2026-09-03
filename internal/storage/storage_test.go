package storage

import (
	"testing"
	"time"
)

func TestMetricAndFlowCRUD(t *testing.T) {
	s, err := OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	defer s.Close()
	base := time.Now().UTC()
	for i, v := range []float64{1, 2, 3} {
		if err := s.AppendMetric(MetricPoint{
			Ts: base.Add(-time.Duration(3-i) * time.Second), Series: "traffic.bps",
			Tags: map[string]string{"machine_id": "m1"}, Value: v,
		}); err != nil {
			t.Fatalf("AppendMetric: %v", err)
		}
	}
	pts, err := s.QueryMetrics("traffic.bps", base.Add(-5*time.Second), base)
	if err != nil || len(pts) != 3 {
		t.Fatalf("QueryMetrics len=%d err=%v, want 3", len(pts), err)
	}

	recs := []FlowRecord{
		{SrcIP: "10.0.0.1", DstIP: "8.8.8.8", SrcPort: 40000, DstPort: 443, Proto: 6,
			Start: base.Add(-10 * time.Second), End: base.Add(-9 * time.Second), Packets: 10, Bytes: 1000, MachineID: "m1"},
		{SrcIP: "10.0.0.1", DstIP: "1.1.1.1", SrcPort: 50000, DstPort: 53, Proto: 17,
			Start: base.Add(-8 * time.Second), End: base.Add(-7 * time.Second), MachineID: "m1"},
		{SrcIP: "8.8.8.8", DstIP: "10.0.0.1", SrcPort: 443, DstPort: 40000, Proto: 6,
			Start: base.Add(-6 * time.Second), End: base.Add(-5 * time.Second), MachineID: "m1"},
	}
	for _, r := range recs {
		if err := s.AppendFlow(r); err != nil {
			t.Fatalf("AppendFlow: %v", err)
		}
	}
	rows, total, err := s.QuerySessions(SessionFilter{IP: "10.0.0.1"}, 10, 0)
	if err != nil || total != 3 {
		t.Fatalf("IP 过滤 total=%d err=%v, want 3", total, err)
	}
	_, total, _ = s.QuerySessions(SessionFilter{IP: "10.0.0.1", Proto: 6}, 10, 0)
	if total != 2 {
		t.Errorf("TCP 过滤 total=%d, want 2", total)
	}
	rows, total, _ = s.QuerySessions(SessionFilter{IP: "10.0.0.1", Proto: 6, Port: 443}, 1, 0)
	if total != 2 || len(rows) != 1 {
		t.Fatalf("分页结果错误: len=%d total=%d", len(rows), total)
	}
	if rows[0].Start != recs[2].Start { // 按开始时间倒序，最新在前
		t.Error("会话未按开始时间倒序返回")
	}
}

func TestPersistenceAndPrune(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()
	s, _ := OpenFileStore(dir, time.Hour)
	old := base.Add(-2 * time.Hour)
	s.AppendMetric(MetricPoint{Ts: old, Series: "traffic.bps", Value: 9})
	s.AppendMetric(MetricPoint{Ts: base, Series: "traffic.bps", Value: 5})
	s.AppendFlow(FlowRecord{Start: base, End: base, SrcIP: "10.0.0.1", DstIP: "8.8.8.8", Proto: 6})
	if err := s.Prune(base); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	s.Close()

	s2, err := OpenFileStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("重开存储: %v", err)
	}
	defer s2.Close()
	pts, _ := s2.QueryMetrics("traffic.bps", old.Add(-time.Minute), base.Add(time.Minute))
	if len(pts) != 1 || pts[0].Value != 5 {
		t.Errorf("保留策略或持久化异常: %+v", pts)
	}
	_, total, _ := s2.QuerySessions(SessionFilter{}, 10, 0)
	if total != 1 {
		t.Errorf("流记录持久化异常: total=%d", total)
	}
}
