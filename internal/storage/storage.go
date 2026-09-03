// Package storage 抽象存储后端并实现 MVP 文件存储（JSONL 追加式持久化）。
//
// 设计说明：技术方案要求 MVP 使用 SQLite、并为多机预留可替换接口；
// 本仓库当前处于离线开发环境（无法拉取第三方依赖），因此先实现
// 与 SQLite 语义等价的 FileStore：内存索引 + JSONL 追加落盘 + 保留策略清理。
// 上层只依赖 Backend 接口，迁移到 SQLite/时序库时无需改动业务代码。
package storage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MetricPoint 时序指标点（单位：后端一律存原始值，换算交给前端）。
type MetricPoint struct {
	Ts     time.Time
	Series string
	Tags   map[string]string
	Value  float64
}

// FlowRecord 会话记录。
type FlowRecord struct {
	Start     time.Time
	End       time.Time
	SrcIP     string
	SrcPort   uint16
	DstIP     string
	DstPort   uint16
	Proto     uint8
	Packets   uint64
	Bytes     uint64
	TCPFlags  uint8
	Iface     string
	MachineID string
}

// SessionFilter 会话查询条件（零值表示不限制）。
type SessionFilter struct {
	IP    string
	Proto uint8
	Port  uint16
	From  time.Time
	To    time.Time
}

// Backend 存储接口：SQLite/InfluxDB/ClickHouse 都可作为实现。
type Backend interface {
	AppendMetric(p MetricPoint) error
	AppendFlow(f FlowRecord) error
	QueryMetrics(series string, from, to time.Time) ([]MetricPoint, error)
	QuerySessions(f SessionFilter, limit, offset int) ([]FlowRecord, int, error)
	Prune(now time.Time) error
	Close() error
}

// FileStore 是 Backend 的 JSONL 文件实现。
type FileStore struct {
	mu        sync.Mutex
	dir       string
	retention time.Duration
	metrics   []MetricPoint
	flows     []FlowRecord
	mf, ff    *os.File
}

// OpenFileStore 打开（不存在则创建）数据目录并加载已有数据。
func OpenFileStore(dir string, retention time.Duration) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &FileStore{dir: dir, retention: retention}
	var err error
	if s.mf, err = os.OpenFile(filepath.Join(dir, "metrics.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
		return nil, err
	}
	if s.ff, err = os.OpenFile(filepath.Join(dir, "flows.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
		s.mf.Close()
		return nil, err
	}
	if err := s.loadMetrics(filepath.Join(dir, "metrics.jsonl")); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.loadFlows(filepath.Join(dir, "flows.jsonl")); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *FileStore) loadMetrics(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		var l metricLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue // 容忍损坏行
		}
		s.metrics = append(s.metrics, MetricPoint{Ts: time.Unix(0, l.Ts).UTC(), Series: l.Series, Tags: l.Tags, Value: l.Value})
	}
	return sc.Err()
}

func (s *FileStore) loadFlows(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		var l flowLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		s.flows = append(s.flows, l.record())
	}
	return sc.Err()
}

// AppendMetric 追加一个指标点并落盘。
func (s *FileStore) AppendMetric(p MetricPoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = append(s.metrics, p)
	return writeLine(s.mf, metricLine{Ts: p.Ts.UnixNano(), Series: p.Series, Tags: p.Tags, Value: p.Value})
}

func (s *FileStore) AppendFlow(f FlowRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows = append(s.flows, f)
	return writeLine(s.ff, flowLineFrom(f))
}

func (s *FileStore) QueryMetrics(series string, from, to time.Time) ([]MetricPoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MetricPoint
	for _, p := range s.metrics {
		if p.Series != series || p.Ts.Before(from) || p.Ts.After(to) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *FileStore) QuerySessions(f SessionFilter, limit, offset int) ([]FlowRecord, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []FlowRecord
	for _, r := range s.flows {
		if f.IP != "" && !strings.Contains(r.SrcIP, f.IP) && !strings.Contains(r.DstIP, f.IP) {
			continue
		}
		if f.Proto != 0 && r.Proto != f.Proto {
			continue
		}
		if f.Port != 0 && r.SrcPort != f.Port && r.DstPort != f.Port {
			continue
		}
		if !f.From.IsZero() && r.Start.Before(f.From) {
			continue
		}
		if !f.To.IsZero() && r.Start.After(f.To) {
			continue
		}
		out = append(out, r)
	}
	total := len(out)
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	if offset > total {
		offset = total
	}
	if limit <= 0 {
		limit = 10
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return out[offset:end], total, nil
}

// Prune 按保留策略清理过期数据并重写文件。
func (s *FileStore) Prune(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := now.Add(-s.retention)
	keepM := s.metrics[:0]
	for _, p := range s.metrics {
		if p.Ts.After(cut) {
			keepM = append(keepM, p)
		}
	}
	s.metrics = keepM
	keepF := s.flows[:0]
	for _, r := range s.flows {
		if r.End.After(cut) {
			keepF = append(keepF, r)
		}
	}
	s.flows = keepF
	return s.rewrite()
}

func (s *FileStore) rewrite() error {
	s.mf.Close()
	s.ff.Close()
	var err error
	s.mf, err = os.Create(filepath.Join(s.dir, "metrics.jsonl"))
	if err != nil {
		return err
	}
	s.ff, err = os.Create(filepath.Join(s.dir, "flows.jsonl"))
	if err != nil {
		return err
	}
	for _, p := range s.metrics {
		if err := writeLine(s.mf, metricLine{Ts: p.Ts.UnixNano(), Series: p.Series, Tags: p.Tags, Value: p.Value}); err != nil {
			return err
		}
	}
	for _, f := range s.flows {
		if err := writeLine(s.ff, flowLineFrom(f)); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.mf.Close(), s.ff.Close())
}

func writeLine(f *os.File, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("写存储文件失败: %w", err)
	}
	return nil
}

type metricLine struct {
	Ts     int64             `json:"ts"`
	Series string            `json:"series"`
	Tags   map[string]string `json:"tags"`
	Value  float64           `json:"value"`
}

type flowLine struct {
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
	SrcIP     string `json:"src_ip"`
	SrcPort   uint16 `json:"src_port"`
	DstIP     string `json:"dst_ip"`
	DstPort   uint16 `json:"dst_port"`
	Proto     uint8  `json:"proto"`
	Packets   uint64 `json:"packets"`
	Bytes     uint64 `json:"bytes"`
	TCPFlags  uint8  `json:"tcp_flags"`
	Iface     string `json:"iface"`
	MachineID string `json:"machine_id"`
}

func flowLineFrom(f FlowRecord) flowLine {
	return flowLine{
		Start: f.Start.UnixNano(), End: f.End.UnixNano(),
		SrcIP: f.SrcIP, SrcPort: f.SrcPort, DstIP: f.DstIP, DstPort: f.DstPort,
		Proto: f.Proto, Packets: f.Packets, Bytes: f.Bytes, TCPFlags: f.TCPFlags,
		Iface: f.Iface, MachineID: f.MachineID,
	}
}

func (l flowLine) record() FlowRecord {
	return FlowRecord{
		Start: time.Unix(0, l.Start).UTC(), End: time.Unix(0, l.End).UTC(),
		SrcIP: l.SrcIP, SrcPort: l.SrcPort, DstIP: l.DstIP, DstPort: l.DstPort,
		Proto: l.Proto, Packets: l.Packets, Bytes: l.Bytes, TCPFlags: l.TCPFlags,
		Iface: l.Iface, MachineID: l.MachineID,
	}
}
