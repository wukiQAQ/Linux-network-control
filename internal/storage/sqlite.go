// SQLite 存储实现：与 FileStore 一样实现 Backend 接口，可无缝替换。
// 驱动使用 modernc.org/sqlite（纯 Go，无 CGO），因此 Windows/Linux 均可编译，
// 且与 FileStore 的 JSONL 方案在语义上等价：指标/流记录分表、保留策略清理。
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore 基于 database/sql 的存储实现。
type SQLiteStore struct {
	db        *sql.DB
	retention time.Duration
}

// OpenSQLiteStore 打开（不存在则创建）SQLite 数据库并初始化表结构。
func OpenSQLiteStore(path string, retention time.Duration) (*SQLiteStore, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 单写者模型：串行化写入避免锁冲突
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, err
	}
	// WAL + NORMAL：兼顾耐久性与写入吞吐（长时间运行时差别很明显）
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	// 开启大小写敏感的 LIKE：IP 只有数字/点/冒号，语义不变，
	// 但能让"前缀 LIKE"用上 idx_flows_src_ip / idx_flows_dst_ip 索引（否则只能全表扫）。
	if _, err := db.Exec(`PRAGMA case_sensitive_like=ON`); err != nil {
		db.Close()
		return nil, err
	}
	schema := []string{
		`CREATE TABLE IF NOT EXISTS metrics (
			ts     INTEGER NOT NULL,
			series TEXT    NOT NULL,
			tags   TEXT    NOT NULL DEFAULT '{}',
			value  REAL    NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_series_ts ON metrics(series, ts)`,
		`CREATE TABLE IF NOT EXISTS flows (
			start      INTEGER NOT NULL,
			end        INTEGER NOT NULL,
			src_ip     TEXT,
			src_port   INTEGER,
			dst_ip     TEXT,
			dst_port   INTEGER,
			proto      INTEGER,
			packets    INTEGER,
			bytes      INTEGER,
			tcp_flags  INTEGER,
			iface      TEXT,
			machine_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_flows_start ON flows(start)`,
		`CREATE INDEX IF NOT EXISTS idx_flows_proto_start ON flows(proto, start)`,
		`CREATE INDEX IF NOT EXISTS idx_flows_src_ip ON flows(src_ip)`,
		`CREATE INDEX IF NOT EXISTS idx_flows_dst_ip ON flows(dst_ip)`,
	}
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("建表失败: %w", err)
		}
	}
	return &SQLiteStore{db: db, retention: retention}, nil
}

func (s *SQLiteStore) AppendMetric(p MetricPoint) error {
	tags, err := json.Marshal(p.Tags)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO metrics(ts, series, tags, value) VALUES(?, ?, ?, ?)`,
		p.Ts.UnixNano(), p.Series, string(tags), p.Value)
	return err
}

// AppendMetrics 在一个事务里批量插入指标：每秒 5 个指标只需一次提交。
func (s *SQLiteStore) AppendMetrics(points []MetricPoint) error {
	if len(points) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO metrics(ts, series, tags, value) VALUES(?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, p := range points {
		tags, terr := json.Marshal(p.Tags)
		if terr != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return terr
		}
		if _, err := stmt.Exec(p.Ts.UnixNano(), p.Series, string(tags), p.Value); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) AppendFlow(f FlowRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO flows(start, end, src_ip, src_port, dst_ip, dst_port,
			proto, packets, bytes, tcp_flags, iface, machine_id)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Start.UnixNano(), f.End.UnixNano(),
		f.SrcIP, f.SrcPort, f.DstIP, f.DstPort, f.Proto,
		f.Packets, f.Bytes, f.TCPFlags, f.Iface, f.MachineID)
	return err
}

func (s *SQLiteStore) QueryMetrics(series string, from, to time.Time) ([]MetricPoint, error) {
	rows, err := s.db.Query(
		`SELECT ts, value FROM metrics WHERE series = ? AND ts BETWEEN ? AND ? ORDER BY ts`,
		series, from.UnixNano(), to.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricPoint
	for rows.Next() {
		var ts int64
		var v float64
		if err := rows.Scan(&ts, &v); err != nil {
			return nil, err
		}
		out = append(out, MetricPoint{Ts: time.Unix(0, ts).UTC(), Series: series, Value: v})
	}
	return out, rows.Err()
}

// flowWhere 生成会话查询的 WHERE 子句与参数。
//
// IP 条件刻意不用 "%x%"：以通配符开头的 LIKE 无法使用索引，只能全表扫描。
// 这里改为两种都能走索引的写法：
//   - 完整 IP（能解析成地址）→ 精确等值，配合 SQLite 的 OR 优化同时用上 idx_flows_src_ip / idx_flows_dst_ip；
//   - 其它（如 "10.0."）→ 前缀区间 [前缀, 前缀上界)，用 >= / < 比较，确定能走索引；
//     语义与文件后端的 strings.HasPrefix 完全一致，用户输入的 %、_ 只是普通字符。
func flowWhere(f SessionFilter) (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if ip := strings.TrimSpace(f.IP); ip != "" {
		if _, err := netip.ParseAddr(ip); err == nil {
			where = append(where, "(src_ip = ? OR dst_ip = ?)")
			args = append(args, ip, ip)
		} else if hi := prefixUpper(ip); hi != "" {
			where = append(where, "((src_ip >= ? AND src_ip < ?) OR (dst_ip >= ? AND dst_ip < ?))")
			args = append(args, ip, hi, ip, hi)
		} else {
			// 前缀已到字节上界（极不可能）：退化为 >= 前缀
			where = append(where, "(src_ip >= ? OR dst_ip >= ?)")
			args = append(args, ip, ip)
		}
	}
	if f.Proto != 0 {
		where = append(where, "proto = ?")
		args = append(args, f.Proto)
	}
	if f.Port != 0 {
		where = append(where, "(src_port = ? OR dst_port = ?)")
		args = append(args, f.Port, f.Port)
	}
	if !f.From.IsZero() {
		where = append(where, "start >= ?")
		args = append(args, f.From.UnixNano())
	}
	if !f.To.IsZero() {
		where = append(where, "start <= ?")
		args = append(args, f.To.UnixNano())
	}
	return strings.Join(where, " AND "), args
}

// prefixUpper 返回"前缀区间"的上界：把最后一个字节 +1（如 "10.0." → "10.0/"）。
// 返回空串表示没有上界（整串都是 0xFF），调用方退化为 >= 前缀。
func prefixUpper(s string) string {
	b := []byte(s)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1])
		}
	}
	return ""
}

// AggregateFlows 在数据库内聚合会话记录：不受"只取前 N 条样本"的限制，占比与旧的内存聚合公式一致。
func (s *SQLiteStore) AggregateFlows(f SessionFilter, limit int) (FlowAggregate, error) {
	if limit <= 0 {
		limit = 10
	}
	cond, args := flowWhere(f)
	var agg FlowAggregate
	if err := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(bytes), 0) FROM flows WHERE `+cond, args...,
	).Scan(&agg.TotalFlows, &agg.TotalBytes); err != nil {
		return FlowAggregate{}, err
	}

	// IP：一条会话的两端各自上榜（与旧行为一致）
	ipUnion := `SELECT src_ip AS key, packets, bytes FROM flows WHERE ` + cond +
		` AND src_ip <> '' AND src_ip <> '0' UNION ALL ` +
		`SELECT dst_ip AS key, packets, bytes FROM flows WHERE ` + cond +
		` AND dst_ip <> '' AND dst_ip <> '0'`
	byIP, err := s.aggUnion(ipUnion, args, limit)
	if err != nil {
		return FlowAggregate{}, err
	}
	agg.ByIP = byIP

	// 协议：按整条会话计入，维度总量就是总字节数
	proto, err := s.aggGrouped(cond, args, `CAST(proto AS TEXT)`, agg.TotalBytes, limit)
	if err != nil {
		return FlowAggregate{}, err
	}
	agg.ByProto = proto

	// 端口：只统计 TCP/UDP 且非 0 的端口，双向计入
	portUnion := `SELECT CAST(src_port AS TEXT) AS key, packets, bytes FROM flows WHERE ` + cond +
		` AND proto IN (6, 17) AND src_port <> 0 UNION ALL ` +
		`SELECT CAST(dst_port AS TEXT) AS key, packets, bytes FROM flows WHERE ` + cond +
		` AND proto IN (6, 17) AND dst_port <> 0`
	byPort, err := s.aggUnion(portUnion, args, limit)
	if err != nil {
		return FlowAggregate{}, err
	}
	agg.ByPort = byPort
	return agg, nil
}

// aggUnion 聚合"双向计入"的维度（IP / 端口）：先算维度总字节，再取前 limit 项。
// inner 里 WHERE 出现两次，因此参数也要重复一遍。
func (s *SQLiteStore) aggUnion(inner string, args []any, limit int) ([]AggEntry, error) {
	unionArgs := append(append([]any{}, args...), args...)
	var total uint64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(bytes), 0) FROM (`+inner+`)`, unionArgs...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT key, COUNT(*), COALESCE(SUM(packets), 0), COALESCE(SUM(bytes), 0) FROM (`+inner+`)`+
			` GROUP BY key ORDER BY SUM(bytes) DESC, key LIMIT ?`, append(append([]any{}, unionArgs...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAggEntries(rows, total)
}

// aggGrouped 聚合普通维度（协议）。
func (s *SQLiteStore) aggGrouped(cond string, args []any, expr string, total uint64, limit int) ([]AggEntry, error) {
	rows, err := s.db.Query(
		`SELECT `+expr+` AS key, COUNT(*), COALESCE(SUM(packets), 0), COALESCE(SUM(bytes), 0) FROM flows WHERE `+cond+
			` GROUP BY key ORDER BY SUM(bytes) DESC, key LIMIT ?`, append(append([]any{}, args...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAggEntries(rows, total)
}

func scanAggEntries(rows *sql.Rows, total uint64) ([]AggEntry, error) {
	out := make([]AggEntry, 0, 16)
	for rows.Next() {
		var e AggEntry
		if err := rows.Scan(&e.Key, &e.Flows, &e.Packets, &e.Bytes); err != nil {
			return nil, err
		}
		e.Percent = percentOf(e.Bytes, total)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) QuerySessions(f SessionFilter, limit, offset int) ([]FlowRecord, int, error) {
	cond, args := flowWhere(f)

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM flows WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 10
	}
	q := `SELECT start, end, src_ip, src_port, dst_ip, dst_port, proto,
		packets, bytes, tcp_flags, iface, machine_id
		FROM flows WHERE ` + cond + ` ORDER BY start DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []FlowRecord
	for rows.Next() {
		var r FlowRecord
		var st, en int64
		if err := rows.Scan(&st, &en, &r.SrcIP, &r.SrcPort, &r.DstIP, &r.DstPort,
			&r.Proto, &r.Packets, &r.Bytes, &r.TCPFlags, &r.Iface, &r.MachineID); err != nil {
			return nil, 0, err
		}
		r.Start = time.Unix(0, st).UTC()
		r.End = time.Unix(0, en).UTC()
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// Prune 按保留策略删除过期数据。
func (s *SQLiteStore) Prune(now time.Time) error {
	cut := now.Add(-s.retention).UnixNano()
	if _, err := s.db.Exec(`DELETE FROM metrics WHERE ts < ?`, cut); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM flows WHERE end < ?`, cut)
	return err
}

func (s *SQLiteStore) Close() error { return s.db.Close() }
