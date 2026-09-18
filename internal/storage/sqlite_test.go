package storage

import (
	"path/filepath"
	"strings"
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

// TestSQLiteBasics 验证基础优化：批量写入可用、WAL+NORMAL 生效、会话索引存在。
func TestSQLiteBasics(t *testing.T) {
	path := t.TempDir() + "/netmon.db"
	st, err := OpenSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatalf("OpenSQLiteStore: %v", err)
	}
	defer st.Close()

	// 1) 批量写入 6 个指标
	now := time.Now().UTC()
	points := []MetricPoint{
		{Ts: now, Series: "traffic.bps", Value: 1},
		{Ts: now, Series: "traffic.pps", Value: 2},
		{Ts: now, Series: "traffic.conns", Value: 3},
		{Ts: now, Series: "traffic.newconns", Value: 4},
		{Ts: now, Series: "traffic.closedconns", Value: 5},
		{Ts: now, Series: "traffic.drops", Value: 6},
	}
	if err := st.AppendMetrics(points); err != nil {
		t.Fatalf("AppendMetrics: %v", err)
	}
	got, err := st.QueryMetrics("traffic.closedconns", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil || len(got) != 1 || got[0].Value != 5 {
		t.Fatalf("批量写入后查询异常: %v %v", got, err)
	}
	// 空切片不应报错
	if err := st.AppendMetrics(nil); err != nil {
		t.Errorf("空批量应直接返回: %v", err)
	}

	// 2) PRAGMA：WAL + synchronous=NORMAL(1)
	var mode string
	if err := st.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || strings.ToLower(mode) != "wal" {
		t.Errorf("journal_mode 应为 wal，实际 %q (%v)", mode, err)
	}
	var sync int
	if err := st.db.QueryRow(`PRAGMA synchronous`).Scan(&sync); err != nil || sync != 1 {
		t.Errorf("synchronous 应为 1(NORMAL)，实际 %d (%v)", sync, err)
	}

	// 3) 索引存在
	for _, idx := range []string{"idx_metrics_series_ts", "idx_flows_start", "idx_flows_proto_start", "idx_flows_src_ip", "idx_flows_dst_ip"} {
		var n int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("索引 %s 不存在 (%v)", idx, err)
		}
	}
}
