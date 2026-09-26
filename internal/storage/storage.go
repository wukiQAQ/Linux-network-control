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
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
// IP 支持三种写法（都会走索引，不再做全表子串匹配）：
//   - 完整 IP（如 10.0.0.5）：精确匹配源或目的；
//   - 前缀（如 10.0.）：匹配源或目的的前缀；
//   - 其它：按前缀处理（不做 "%x%" 子串匹配）。
type SessionFilter struct {
	IP    string
	Proto uint8
	Port  uint16
	From  time.Time
	To    time.Time
}

// AggEntry 是一个聚合分组（Key 的含义由维度决定：IP / 协议号 / 端口号）。
type AggEntry struct {
	Key     string
	Flows   int
	Packets uint64
	Bytes   uint64
	Percent float64 // 在该维度总量中的字节占比（0~100）
}

// FlowAggregate 是会话聚合结果：IP 与端口按双向计入（一条会话两端都上榜），协议按整条会话计入。
type FlowAggregate struct {
	TotalFlows int
	TotalBytes uint64
	ByIP       []AggEntry
	ByProto    []AggEntry
	ByPort     []AggEntry
}

// Backend 存储接口：SQLite/InfluxDB/ClickHouse 都可作为实现。
type Backend interface {
	AppendMetric(p MetricPoint) error
	// AppendMetrics 批量写入指标（一次事务/一次刷盘，显著减少长时间运行的写放大）
	AppendMetrics(points []MetricPoint) error
	AppendFlow(f FlowRecord) error
	QueryMetrics(series string, from, to time.Time) ([]MetricPoint, error)
	QuerySessions(f SessionFilter, limit, offset int) ([]FlowRecord, int, error)
	// AggregateFlows 按维度聚合会话记录。
	// SQL 后端在数据库内聚合（不受"只取前 N 条样本"限制），文件后端在内存里做等价聚合。
	AggregateFlows(f SessionFilter, limit int) (FlowAggregate, error)
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

// AppendMetrics 批量追加指标（文件后端逐条追加，一次调用即可）。
func (s *FileStore) AppendMetrics(points []MetricPoint) error {
	for _, p := range points {
		if err := s.AppendMetric(p); err != nil {
			return err
		}
	}
	return nil
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
		// 与 SQLite 版共用 matchFlow：完整 IP 精确匹配、其它按前缀匹配（不再做子串匹配）
		if !matchFlow(r, f) {
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

// rewrite 把清理后的数据写回文件：先写临时文件再原子 rename，
// 因此即使中途失败，原文件仍然完整可用（此前是直接截断重写，风险较高）。
func (s *FileStore) rewrite() error {
	if err := s.mf.Close(); err != nil {
		return err
	}
	if err := s.ff.Close(); err != nil {
		return err
	}
	if err := s.rewriteOne("metrics.jsonl", func(f *os.File) error {
		for _, p := range s.metrics {
			if err := writeLine(f, metricLine{Ts: p.Ts.UnixNano(), Series: p.Series, Tags: p.Tags, Value: p.Value}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := s.rewriteOne("flows.jsonl", func(f *os.File) error {
		for _, rec := range s.flows {
			if err := writeLine(f, flowLineFrom(rec)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	var err error
	if s.mf, err = os.OpenFile(filepath.Join(s.dir, "metrics.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
		return err
	}
	if s.ff, err = os.OpenFile(filepath.Join(s.dir, "flows.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
		return err
	}
	return nil
}

// rewriteOne 写临时文件并原子替换目标文件。
func (s *FileStore) rewriteOne(name string, fill func(*os.File) error) error {
	target := filepath.Join(s.dir, name)
	tmp := target + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := fill(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, target)
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

// ---------- 会话聚合（供排行使用） ----------

type aggCounter struct {
	flows   int
	packets uint64
	bytes   uint64
}

func addAgg(m map[string]*aggCounter, key string, r FlowRecord) {
	if key == "" || key == "0" {
		return
	}
	c := m[key]
	if c == nil {
		c = &aggCounter{}
		m[key] = c
	}
	c.flows++
	c.packets += r.Packets
	c.bytes += r.Bytes
}

// percentOf 计算"某项字节数占该维度总字节数"的百分比（0~100，保留 1 位小数）。
// SQL 聚合与内存聚合共用它，保证两条路径给出的占比完全一致。
func percentOf(bytes, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(int64(bytes)*1000/int64(total)) / 10
}

// finalizeAgg 排序、截断并计算占比（占比相对该维度的总字节数）。
func finalizeAgg(m map[string]*aggCounter, limit int) []AggEntry {
	var total uint64
	for _, c := range m {
		total += c.bytes
	}
	list := make([]AggEntry, 0, len(m))
	for k, c := range m {
		list = append(list, AggEntry{
			Key: k, Flows: c.flows, Packets: c.packets, Bytes: c.bytes,
			Percent: percentOf(c.bytes, total),
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Bytes != list[j].Bytes {
			return list[i].Bytes > list[j].Bytes
		}
		return list[i].Key < list[j].Key
	})
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list
}

// aggregateRecords 在内存里聚合会话记录：与 SQLite 版的聚合语义保持一致。
func aggregateRecords(records []FlowRecord, limit int) FlowAggregate {
	if limit <= 0 {
		limit = 10
	}
	agg := FlowAggregate{TotalFlows: len(records)}
	ip := map[string]*aggCounter{}
	proto := map[string]*aggCounter{}
	port := map[string]*aggCounter{}
	for _, r := range records {
		agg.TotalBytes += r.Bytes
		addAgg(ip, r.SrcIP, r)
		addAgg(ip, r.DstIP, r)
		addAgg(proto, strconv.Itoa(int(r.Proto)), r)
		if r.Proto == 6 || r.Proto == 17 {
			if r.SrcPort != 0 {
				addAgg(port, strconv.Itoa(int(r.SrcPort)), r)
			}
			if r.DstPort != 0 {
				addAgg(port, strconv.Itoa(int(r.DstPort)), r)
			}
		}
	}
	agg.ByIP = finalizeAgg(ip, limit)
	agg.ByProto = finalizeAgg(proto, limit)
	agg.ByPort = finalizeAgg(port, limit)
	return agg
}

// AggregateFlows 在内存里过滤并聚合（FileStore 没有数据库聚合能力，语义与 SQLite 版一致）。
func (s *FileStore) AggregateFlows(f SessionFilter, limit int) (FlowAggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]FlowRecord, 0, len(s.flows))
	for _, r := range s.flows {
		if !matchFlow(r, f) {
			continue
		}
		matched = append(matched, r)
	}
	return aggregateRecords(matched, limit), nil
}

// matchFlow 判断一条会话是否满足过滤条件（与 SQL 版语义一致：完整 IP 精确匹配、其它前缀匹配）。
func matchFlow(r FlowRecord, f SessionFilter) bool {
	if ip := strings.TrimSpace(f.IP); ip != "" {
		if !matchIP(r.SrcIP, ip) && !matchIP(r.DstIP, ip) {
			return false
		}
	}
	if f.Proto != 0 && r.Proto != f.Proto {
		return false
	}
	if f.Port != 0 && r.SrcPort != f.Port && r.DstPort != f.Port {
		return false
	}
	if !f.From.IsZero() && r.Start.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && r.Start.After(f.To) {
		return false
	}
	return true
}

// matchIP 支持"完整 IP 精确匹配"与"前缀匹配"两种写法。
// 必须与 SQLite 版（flowWhere）保持一致：能解析成地址的输入只做等值比较，
// 否则 "10.0.0.1" 会在文件后端顺带命中 "10.0.0.10"，两个后端结果就不一样了。
func matchIP(addr, want string) bool {
	if addr == "" || want == "" {
		return false
	}
	if addr == want {
		return true
	}
	if _, err := netip.ParseAddr(want); err == nil {
		return false
	}
	return strings.HasPrefix(addr, want)
}
