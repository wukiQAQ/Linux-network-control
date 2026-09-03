package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteMetricAndFlow(t *testing.T) {
	s, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "netmon.db"), time.Hour)
	if err != nil {
		t.Fatalf("OpenSQLiteStore: %v", err)
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
		t.Fatalf("QueryMetrics len=%d err=%v", len(pts), err)
	}
	for i := 0; i < 3; i++ {
		if err := s.AppendFlow(FlowRecord{
			SrcIP: "10.0.0.1", DstIP: "8.8.8.8", SrcPort: 40000, DstPort: 443,
			Proto: 6, Start: base.Add(-10 * time.Second), End: base.Add(-9 * time.Second),
			Packets: 10, Bytes: 1000, MachineID: "m1",
		}); err != nil {
			t.Fatalf("AppendFlow: %v", err)
		}
	}
	rows, total, err := s.QuerySessions(SessionFilter{IP: "10.0.0.1", Proto: 6}, 2, 0)
	if err != nil || total != 3 || len(rows) != 2 {
		t.Fatalf("查询异常: len=%d total=%d err=%v", len(rows), total, err)
	}
	if err := s.Prune(base.Add(24 * time.Hour)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
}
