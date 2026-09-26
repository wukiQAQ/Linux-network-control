package storage

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// aggFixture 覆盖：双向计入（IP/端口）、TCP/UDP/ICMP 协议分布、ICMP 无端口。
func aggFixture(base time.Time) []FlowRecord {
	return []FlowRecord{
		{Start: base, End: base.Add(time.Second), SrcIP: "10.0.0.1", SrcPort: 40000, DstIP: "8.8.8.8", DstPort: 443, Proto: 6, Packets: 10, Bytes: 1000},
		{Start: base.Add(time.Second), End: base.Add(2 * time.Second), SrcIP: "10.0.0.1", SrcPort: 40001, DstIP: "1.1.1.1", DstPort: 53, Proto: 17, Packets: 2, Bytes: 200},
		{Start: base.Add(2 * time.Second), End: base.Add(3 * time.Second), SrcIP: "10.0.0.2", SrcPort: 40002, DstIP: "8.8.8.8", DstPort: 443, Proto: 6, Packets: 5, Bytes: 500},
		{Start: base.Add(3 * time.Second), End: base.Add(4 * time.Second), SrcIP: "10.0.0.9", DstIP: "10.0.0.10", Proto: 1, Packets: 1, Bytes: 64},
	}
}

// checkAggregate 校验聚合语义（SQLite 与文件后端必须给出完全一致的结果）。
func checkAggregate(t *testing.T, agg FlowAggregate) {
	t.Helper()
	if agg.TotalFlows != 4 || agg.TotalBytes != 1764 {
		t.Fatalf("总量错误: flows=%d bytes=%d", agg.TotalFlows, agg.TotalBytes)
	}
	// IP：双向计入，维度总量 = 2 × 总字节
	if len(agg.ByIP) != 2 {
		t.Fatalf("TOP IP 应被 limit 截断为 2 项: %+v", agg.ByIP)
	}
	if agg.ByIP[0].Key != "8.8.8.8" || agg.ByIP[0].Bytes != 1500 || agg.ByIP[0].Flows != 2 {
		t.Errorf("TOP IP 第一名错误: %+v", agg.ByIP[0])
	}
	if agg.ByIP[1].Key != "10.0.0.1" || agg.ByIP[1].Bytes != 1200 {
		t.Errorf("TOP IP 第二名错误: %+v", agg.ByIP[1])
	}
	if p := agg.ByIP[0].Percent; p < 42.4 || p > 42.6 {
		t.Errorf("IP 占比应约 42.5%%（1500/3528），实际 %v", p)
	}
	// 协议：按整条会话计入，维度总量 = 总字节
	if len(agg.ByProto) != 2 || agg.ByProto[0].Key != "6" || agg.ByProto[0].Bytes != 1500 {
		t.Fatalf("协议聚合错误: %+v", agg.ByProto)
	}
	if agg.ByProto[1].Key != "17" || agg.ByProto[1].Bytes != 200 {
		t.Errorf("协议第二名应为 17/UDP: %+v", agg.ByProto[1])
	}
	if p := agg.ByProto[0].Percent; p < 85.0 || p > 85.1 {
		t.Errorf("TCP 占比应约 85.0%%（1500/1764），实际 %v", p)
	}
	// 端口：只统计 TCP/UDP 且非 0 的端口，ICMP 不参与
	if len(agg.ByPort) != 2 || agg.ByPort[0].Key != "443" || agg.ByPort[0].Bytes != 1500 {
		t.Fatalf("端口聚合错误: %+v", agg.ByPort)
	}
	if agg.ByPort[1].Key != "40000" || agg.ByPort[1].Bytes != 1000 {
		t.Errorf("端口第二名应为 40000: %+v", agg.ByPort[1])
	}
	for _, e := range agg.ByPort {
		if e.Key == "53" {
			t.Errorf("limit=2 时不应出现 53: %+v", agg.ByPort)
		}
	}
}

// TestAggregateFlowsSQLite 校验数据库内聚合（不再有样本上限）。
func TestAggregateFlowsSQLite(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	store, err := OpenSQLiteStore(t.TempDir()+"/agg.db", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, r := range aggFixture(base) {
		if err := store.AppendFlow(r); err != nil {
			t.Fatal(err)
		}
	}
	agg, err := store.AggregateFlows(SessionFilter{}, 2)
	if err != nil {
		t.Fatalf("AggregateFlows: %v", err)
	}
	checkAggregate(t, agg)

	// limit=10：ICMP（proto=1）此时应出现在协议榜，但仍不得进入端口榜
	full, err := store.AggregateFlows(SessionFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.ByPort) != 5 {
		t.Errorf("端口维度应只有 5 个（TCP/UDP 双向），实际 %+v", full.ByPort)
	}
	found := false
	for _, e := range full.ByProto {
		if e.Key == "1" {
			found = true
		}
	}
	if !found {
		t.Errorf("协议榜应包含 ICMP（key=1）: %+v", full.ByProto)
	}

	// 时间过滤：只取最后 1.5 秒（应剩 ICMP 那条）
	recent, err := store.AggregateFlows(SessionFilter{From: base.Add(2900 * time.Millisecond)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if recent.TotalFlows != 1 || recent.TotalBytes != 64 {
		t.Errorf("时间过滤错误: %+v", recent)
	}
}

// TestAggregateFlowsFileParity 校验文件后端与 SQLite 后端给出完全一致的聚合结果。
func TestAggregateFlowsFileParity(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	dir := t.TempDir()
	sqliteStore, err := OpenSQLiteStore(dir+"/agg.db", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteStore.Close()
	fileStore, err := OpenFileStore(dir+"/files", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer fileStore.Close()
	for _, r := range aggFixture(base) {
		if err := sqliteStore.AppendFlow(r); err != nil {
			t.Fatal(err)
		}
		if err := fileStore.AppendFlow(r); err != nil {
			t.Fatal(err)
		}
	}
	gotSQL, err := sqliteStore.AggregateFlows(SessionFilter{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	gotFile, err := fileStore.AggregateFlows(SessionFilter{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	checkAggregate(t, gotSQL)
	if !reflect.DeepEqual(gotSQL, gotFile) {
		t.Errorf("两个后端聚合结果不一致:\nSQLite=%+v\nFile =%+v", gotSQL, gotFile)
	}
}

// TestFlowWhereIsIndexFriendly 校验 IP 条件用的是"精确等值"或"前缀区间"，而不是首尾通配的 LIKE。
func TestFlowWhereIsIndexFriendly(t *testing.T) {
	cond, args := flowWhere(SessionFilter{IP: "10.0.0.5"})
	if !strings.Contains(cond, "src_ip = ? OR dst_ip = ?") {
		t.Errorf("完整 IP 应用精确等值: %s", cond)
	}
	if len(args) != 2 || args[0] != "10.0.0.5" || args[1] != "10.0.0.5" {
		t.Errorf("完整 IP 的参数错误: %v", args)
	}

	cond, args = flowWhere(SessionFilter{IP: "10.0."})
	if strings.Contains(cond, "LIKE") {
		t.Errorf("不应再使用 LIKE: %s", cond)
	}
	if !strings.Contains(cond, "src_ip >= ? AND src_ip < ?") {
		t.Errorf("前缀应使用区间查询: %s", cond)
	}
	if len(args) != 4 || args[0] != "10.0." || args[1] != "10.0/" {
		t.Errorf("前缀区间参数错误: %v", args)
	}

	// 用户输入的 % 与 _ 只是普通字符（不再当通配符）
	cond, args = flowWhere(SessionFilter{IP: "10.0%_"})
	if strings.Contains(cond, "LIKE") || len(args) != 4 || args[0] != "10.0%_" {
		t.Errorf("通配符应按字面量处理: %s / %v", cond, args)
	}

	// 前缀上界：末位是 0xff 时向前进位
	if got := prefixUpper("10.0.\xff"); got != "10.0/" {
		t.Errorf("prefixUpper 进位错误: %q", got)
	}
	if got := prefixUpper("\xff\xff"); got != "" {
		t.Errorf("全 0xff 应无上界: %q", got)
	}

	// IPv6 精确匹配
	cond, _ = flowWhere(SessionFilter{IP: "2001:db8::1"})
	if !strings.Contains(cond, "src_ip = ? OR dst_ip = ?") {
		t.Errorf("IPv6 完整地址应精确匹配: %s", cond)
	}
}

// TestQuerySessionsResultAndIndex 校验查询结果与"确实用上了 IP 索引"。
func TestQuerySessionsResultAndIndex(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	store, err := OpenSQLiteStore(t.TempDir()+"/q.db", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, r := range aggFixture(base) {
		if err := store.AppendFlow(r); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		ip    string
		total int
	}{
		{"10.0.0.1", 2}, // 精确
		{"10.0.0.", 4},  // 前缀：夹具 4 行全部命中（源 10.0.0.1/2/9 三行 + 第 4 行目的 10.0.0.10），按行计数不重复
		{"8.8.8.8", 2},  // 精确（目的地址）
		{"1.1.1.1", 1},  // 精确
		{"0.0.1", 0},    // 子串不再匹配（语义变更）
	}
	for _, c := range cases {
		_, total, err := store.QuerySessions(SessionFilter{IP: c.ip}, 10, 0)
		if err != nil {
			t.Fatalf("QuerySessions(%q): %v", c.ip, err)
		}
		if total != c.total {
			t.Errorf("IP=%q 命中 %d 条，期望 %d", c.ip, total, c.total)
		}
	}

	// EXPLAIN QUERY PLAN：确认走的是 idx_flows_src_ip / idx_flows_dst_ip
	for _, ip := range []string{"10.0.0.1", "10.0."} {
		cond, args := flowWhere(SessionFilter{IP: ip})
		rows, err := store.db.Query(`EXPLAIN QUERY PLAN SELECT COUNT(*) FROM flows WHERE `+cond, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail + "\n")
		}
		rows.Close()
		got := plan.String()
		if !strings.Contains(got, "idx_flows_src_ip") || !strings.Contains(got, "idx_flows_dst_ip") {
			t.Errorf("IP=%q 的查询计划未使用 IP 索引:\n%s", ip, got)
		}
	}
}

// TestQuerySessionsBackendParity 校验 IP 过滤在两个后端语义完全一致：
// 完整 IP 只做精确匹配（因此 "10.0.0.1" 不会命中 "10.0.0.10"），其它按前缀匹配，输入两侧空白被忽略。
func TestQuerySessionsBackendParity(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	dir := t.TempDir()
	sqliteStore, err := OpenSQLiteStore(dir+"/parity.db", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteStore.Close()
	fileStore, err := OpenFileStore(dir+"/parity", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer fileStore.Close()
	for _, r := range aggFixture(base) {
		if err := sqliteStore.AppendFlow(r); err != nil {
			t.Fatal(err)
		}
		if err := fileStore.AppendFlow(r); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		ip    string
		total int
	}{
		{"10.0.0.1", 2},   // 完整 IP：精确匹配（源 2 条），不含目的 10.0.0.10
		{"10.0.0.10", 1},  // 完整 IP 命中目的地址
		{"10.0.0.", 4},    // 前缀：夹具 4 行全部命中
		{"10.0.", 4},      // 更短的前缀
		{" 10.0.0.1 ", 2}, // 前后空白应被忽略
		{"0.0.1", 0},      // 子串不再匹配（语义变更）
		{"8.8.8.8", 2},    // 目的地址精确匹配
	}
	for _, c := range cases {
		_, sqlTotal, err := sqliteStore.QuerySessions(SessionFilter{IP: c.ip}, 10, 0)
		if err != nil {
			t.Fatalf("SQLite QuerySessions(%q): %v", c.ip, err)
		}
		_, fileTotal, err := fileStore.QuerySessions(SessionFilter{IP: c.ip}, 10, 0)
		if err != nil {
			t.Fatalf("File QuerySessions(%q): %v", c.ip, err)
		}
		if sqlTotal != c.total || fileTotal != c.total {
			t.Errorf("IP=%q SQLite 命中 %d、文件后端命中 %d，期望 %d", c.ip, sqlTotal, fileTotal, c.total)
		}
	}

	// 聚合接口同样要受 IP 过滤影响，且两个后端一致
	sqlAgg, err := sqliteStore.AggregateFlows(SessionFilter{IP: "10.0.0.1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	fileAgg, err := fileStore.AggregateFlows(SessionFilter{IP: "10.0.0.1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sqlAgg, fileAgg) {
		t.Errorf("IP 过滤后的聚合结果不一致:\nSQLite=%+v\nFile  =%+v", sqlAgg, fileAgg)
	}
	if sqlAgg.TotalFlows != 2 {
		t.Errorf("IP=10.0.0.1 的聚合应只含 2 条会话，实际 %d", sqlAgg.TotalFlows)
	}
}
