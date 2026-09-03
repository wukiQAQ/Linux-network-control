// SQLite 存储实现：与 FileStore 一样实现 Backend 接口，可无缝替换。
// 驱动使用 modernc.org/sqlite（纯 Go，无 CGO），因此 Windows/Linux 均可编译，
// 且与 FileStore 的 JSONL 方案在语义上等价：指标/流记录分表、保留策略清理。
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
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

func (s *SQLiteStore) QuerySessions(f SessionFilter, limit, offset int) ([]FlowRecord, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.IP != "" {
		where = append(where, "(src_ip LIKE ? OR dst_ip LIKE ?)")
		args = append(args, "%"+f.IP+"%", "%"+f.IP+"%")
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
	cond := strings.Join(where, " AND ")

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
