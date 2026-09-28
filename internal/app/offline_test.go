package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/aggregator"
	"github.com/wukiQAQ/Linux-network-control/internal/capture"
	"github.com/wukiQAQ/Linux-network-control/internal/config"
	"github.com/wukiQAQ/Linux-network-control/internal/flow"
	"github.com/wukiQAQ/Linux-network-control/internal/parser"
	"github.com/wukiQAQ/Linux-network-control/internal/storage"
)

// offlineLogDone 是 runOffline 在数据源耗尽、残留流落盘后打出的完成日志。
// 测试用它作为"回放已完成"的确定性信号（比 sleep 或轮询落盘计数更可靠）。
const offlineLogDone = "离线回放完成"

// logCapture 并发安全地收集 log 输出，供测试等待完成信号与断言日志内容。
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// captureLog 把标准 logger 的输出改到内存，返回收集器与还原函数。
func captureLog() (*logCapture, func()) {
	c := &logCapture{}
	log.SetOutput(c)
	return c, func() { log.SetOutput(os.Stderr) }
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

// TestOfflineClockAdvance 单测离线数据时钟：用报文时间戳推进聚合周期，
// 并保证 tick 时间永远落在 [首个报文, 当前报文] 区间内。
func TestOfflineClockAdvance(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	tick := time.Second
	c := &offlineClock{}

	// 首帧只建立基线，不产生空采样
	if now, ok := c.advance(base, tick); ok || !now.IsZero() {
		t.Fatalf("首帧不应触发聚合: now=%v ok=%v", now, ok)
	}
	// 数据时间前进不足一个周期：不聚合
	if now, ok := c.advance(base.Add(500*time.Millisecond), tick); ok {
		t.Fatalf("前进 0.5s 不应触发聚合: now=%v", now)
	}
	// 前进 ≥ 一个周期：聚合，时间对齐到 base+tick
	now, ok := c.advance(base.Add(1200*time.Millisecond), tick)
	if !ok || !now.Equal(base.Add(tick)) {
		t.Fatalf("应聚合且对齐到 base+1s: now=%v ok=%v", now, ok)
	}
	// 报文时间跳跃：tick 时间不越过当前报文时间
	now, ok = c.advance(base.Add(10*time.Second), tick)
	if !ok || !now.Equal(base.Add(2*time.Second)) {
		t.Fatalf("跳跃时 tick 应停在 base+2s: now=%v ok=%v", now, ok)
	}
	// 非法输入：零时间戳 / 非正周期，均不聚合也不污染时钟
	if _, ok := c.advance(time.Time{}, tick); ok {
		t.Error("零时间戳不应触发聚合")
	}
	if _, ok := c.advance(base.Add(30*time.Second), 0); ok {
		t.Error("非正周期不应触发聚合")
	}
	// 收尾 tick 用最后一个报文的数据时间
	final, ok := c.final()
	if !ok || !final.Equal(base.Add(10*time.Second)) {
		t.Fatalf("收尾时间=%v ok=%v, want base+10s", final, ok)
	}
	// 一个包都没有时无需收尾
	if _, ok := (&offlineClock{}).final(); ok {
		t.Error("空数据源不应产生收尾 tick")
	}
}

// TestOfflineReplayPipeline 离线回放整链：真实跑一遍 App.Run，
// 断言"回放跑完进程不退出""flows 非空""最后一个采样 bps/pps 非零"，
// 且所有落盘时间戳都落在 pcap 的时间区间内（而不是当前墙钟年份）。
func TestOfflineReplayPipeline(t *testing.T) {
	cap, restore := captureLog()
	defer restore()

	base := time.Unix(1700000000, 0).UTC()
	aIP := netip.MustParseAddr("10.0.0.1")
	bIP := netip.MustParseAddr("8.8.8.8")
	// 24 个报文，每 500ms 一个，跨越 12s 数据时间；每 4 个报文换一条五元组 → 6 条会话
	const wantFlows = 6
	var pkts []capture.Packet
	for i := 0; i < 24; i++ {
		ts := base.Add(time.Duration(i) * 500 * time.Millisecond)
		pkts = append(pkts, capture.Packet{
			Ts:  ts,
			Raw: parser.BuildEthIPv4TCP(aIP, bIP, uint16(40000+i/4), 443, []byte("offline-payload"), true),
		})
	}
	end := pkts[len(pkts)-1].Ts

	path := filepath.Join(t.TempDir(), "offline.pcap")
	if err := capture.WritePCAP(path, pkts); err != nil {
		t.Fatalf("WritePCAP: %v", err)
	}
	src, err := capture.OpenPcapFile(path)
	if err != nil {
		t.Fatalf("OpenPcapFile: %v", err)
	}
	defer src.Close()

	dir := t.TempDir()
	store, err := storage.OpenFileStore(dir, 24*time.Hour)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	defer store.Close()

	cfg := config.Default()
	cfg.Source = "replay"
	cfg.ReplayFile = path
	cfg.Offline = true
	cfg.Tick = time.Second

	table := flow.NewTable(30*time.Second, 5*time.Minute, nil)
	a := New(cfg, src, table, aggregator.New(3600), store)
	if !a.Offline {
		t.Fatal("App.Offline 应跟随 cfg.Offline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- a.Run(ctx) }()

	// ① 等待回放跑完（完成日志在收尾 tick + 残留流落盘之后才打出）
	waitUntil(t, 30*time.Second, func() bool { return cap.contains(offlineLogDone) }, "离线回放完成日志")
	if got := src.Stats().Packets; got != uint64(len(pkts)) {
		t.Fatalf("回放报文数=%d, want %d", got, len(pkts))
	}

	// ② 源耗尽后 Run 必须仍阻塞（进程存活），不能自动返回
	select {
	case err := <-runErr:
		t.Fatalf("离线回放跑完后 Run 不应返回，实际 err=%v", err)
	case <-time.After(300 * time.Millisecond):
	}

	// ③ flows 非空：6 条会话全部落盘
	_, total, err := store.QuerySessions(storage.SessionFilter{}, 100, 0)
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if total != wantFlows {
		t.Fatalf("落盘会话数=%d, want %d", total, wantFlows)
	}

	// ④ metrics 非空，且最后一个采样 bps/pps 至少有一条 > 0
	bps, _ := store.QueryMetrics("traffic.bps", base.Add(-time.Hour), end.Add(time.Hour))
	pps, _ := store.QueryMetrics("traffic.pps", base.Add(-time.Hour), end.Add(time.Hour))
	if len(bps) == 0 || len(pps) == 0 {
		t.Fatalf("metrics 为空: bps=%d pps=%d", len(bps), len(pps))
	}
	lastBps, lastPps := bps[len(bps)-1], pps[len(pps)-1]
	if lastBps.Value <= 0 && lastPps.Value <= 0 {
		t.Fatalf("最后一个采样仍为 0: bps=%v pps=%v", lastBps.Value, lastPps.Value)
	}

	// ⑤ 所有指标时间戳落在 pcap 区间内，且不是墙钟
	for _, p := range bps {
		if p.Ts.Before(base) || p.Ts.After(end) {
			t.Fatalf("指标时间戳越界: %v 不在 [%v, %v]", p.Ts, base, end)
		}
	}
	if lastBps.Ts.Year() == time.Now().UTC().Year() && !lastBps.Ts.Equal(end) {
		t.Errorf("指标时间戳疑似用了墙钟: %v", lastBps.Ts)
	}

	// ⑥ 会话时间戳同样落在 pcap 区间内
	rows, _, _ := store.QuerySessions(storage.SessionFilter{}, 100, 0)
	for _, r := range rows {
		if r.Start.Before(base) || r.Start.After(end) || r.End.Before(base) || r.End.After(end) {
			t.Fatalf("会话时间戳越界: start=%v end=%v 不在 [%v, %v]", r.Start, r.End, base, end)
		}
	}

	// ⑦ ctx 取消后 Run 才返回 nil
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("ctx 取消后 Run 应返回 nil，实际 %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 Run 未返回")
	}
}

// TestOfflineReplayRealPcap 用仓库内真实 pcap 走一遍离线链路：
// 历史曲线非空、最后一条采样非零、时间戳全部落在 pcap 区间内。
func TestOfflineReplayRealPcap(t *testing.T) {
	path := smallestRepoPcap(t)
	first, last, n := pcapTimeRange(t, path)
	if n < 10 {
		t.Skipf("真实 pcap 报文太少（%d），跳过", n)
	}
	t.Logf("真实 pcap=%s 报文=%d 时间区间=[%s, %s]", filepath.Base(path), n, first, last)

	cap, restore := captureLog()
	defer restore()

	src, err := capture.OpenPcapFile(path)
	if err != nil {
		t.Fatalf("OpenPcapFile: %v", err)
	}
	defer src.Close()

	store, err := storage.OpenFileStore(t.TempDir(), 24*time.Hour)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	defer store.Close()

	cfg := config.Default()
	cfg.Source = "replay"
	cfg.ReplayFile = path
	cfg.Offline = true
	cfg.Tick = time.Second

	table := flow.NewTable(30*time.Second, 5*time.Minute, nil)
	a := New(cfg, src, table, aggregator.New(3600), store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- a.Run(ctx) }()

	waitUntil(t, 60*time.Second, func() bool { return cap.contains(offlineLogDone) }, "离线回放完成日志")
	select {
	case err := <-runErr:
		t.Fatalf("回放跑完后 Run 不应返回，实际 err=%v", err)
	case <-time.After(300 * time.Millisecond):
	}

	_, flowTotal, err := store.QuerySessions(storage.SessionFilter{}, 1, 0)
	if err != nil || flowTotal == 0 {
		t.Fatalf("落盘会话数为空: total=%d err=%v", flowTotal, err)
	}
	bps, _ := store.QueryMetrics("traffic.bps", first.Add(-24*time.Hour), last.Add(24*time.Hour))
	pps, _ := store.QueryMetrics("traffic.pps", first.Add(-24*time.Hour), last.Add(24*time.Hour))
	if len(bps) == 0 || len(pps) == 0 {
		t.Fatalf("真实 pcap 回放后 metrics 为空: bps=%d pps=%d", len(bps), len(pps))
	}
	lastBps, lastPps := bps[len(bps)-1], pps[len(pps)-1]
	t.Logf("落盘会话=%d metrics bps=%d 条 pps=%d 条；最后采样 bps=%.0f pps=%.0f ts=%s",
		flowTotal, len(bps), len(pps), lastBps.Value, lastPps.Value, lastBps.Ts.Format(time.RFC3339))
	if lastBps.Value <= 0 && lastPps.Value <= 0 {
		t.Fatalf("最后一个采样仍为 0: bps=%v pps=%v", lastBps.Value, lastPps.Value)
	}
	for _, p := range bps {
		if p.Ts.Before(first) || p.Ts.After(last) {
			t.Fatalf("指标时间戳越界: %v 不在 pcap 区间 [%v, %v]", p.Ts, first, last)
		}
	}
	rows, _, _ := store.QuerySessions(storage.SessionFilter{}, 500, 0)
	for _, r := range rows {
		if r.Start.Before(first) || r.Start.After(last) || r.End.Before(first) || r.End.After(last) {
			t.Fatalf("会话时间戳越界: start=%v end=%v 不在 [%v, %v]", r.Start, r.End, first, last)
		}
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("ctx 取消后 Run 应返回 nil，实际 %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 Run 未返回")
	}
}

// TestOnlineReplayStillReturnsOnSourceEnd 在线路径回归：Offline=false 时
// 数据源结束即排空返回，不引入任何阻塞等待。
func TestOnlineReplayStillReturnsOnSourceEnd(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	pkts := []capture.Packet{
		{Ts: base, Raw: parser.BuildEthIPv4TCP(netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("8.8.8.8"), 40000, 443, []byte("a"), true)},
		{Ts: base.Add(time.Second), Raw: parser.BuildEthIPv4TCP(netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("8.8.8.8"), 40000, 443, []byte("bb"), false)},
	}
	path := filepath.Join(t.TempDir(), "online.pcap")
	if err := capture.WritePCAP(path, pkts); err != nil {
		t.Fatalf("WritePCAP: %v", err)
	}
	src, err := capture.OpenPcapFile(path)
	if err != nil {
		t.Fatalf("OpenPcapFile: %v", err)
	}
	defer src.Close()

	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.Default()
	cfg.Source = "replay"
	cfg.ReplayFile = path
	cfg.Offline = false

	a := New(cfg, src, flow.NewTable(30*time.Second, 5*time.Minute, nil), aggregator.New(100), store)
	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("在线路径源结束应返回 nil，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("在线路径应在数据源结束后返回，不得阻塞等待")
	}
}

// errSource 先吐一个报文，随后返回一个非 EOF 错误，模拟 live 源意外结束。
type errSource struct {
	pkt  *capture.Packet
	err  error
	sent bool
}

func (s *errSource) Next(ctx context.Context) (*capture.Packet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.sent {
		s.sent = true
		return s.pkt, nil
	}
	return nil, s.err
}
func (s *errSource) Close() error         { return nil }
func (s *errSource) Stats() capture.Stats { return capture.Stats{} }

// TestOnlineRunReturnsSourceError 在线路径下数据源意外报错时，错误应上抛给 main。
func TestOnlineRunReturnsSourceError(t *testing.T) {
	boom := errors.New("模拟 live 源异常")
	src := &errSource{
		pkt: &capture.Packet{
			Ts:  time.Unix(1700000000, 0).UTC(),
			Raw: parser.BuildEthIPv4UDP(netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("8.8.8.8"), 50000, 53, []byte("q")),
		},
		err: boom,
	}
	store, err := storage.OpenFileStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.Default()
	a := New(cfg, src, flow.NewTable(time.Minute, 5*time.Minute, nil), aggregator.New(10), store)
	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()
	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("应上抛数据源错误，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("数据源报错后 Run 应返回")
	}
}

// smallestRepoPcap 返回 data/dumps 下体积最小的 pcap（仓库内真实抓包，测试更快）。
// 该目录被 .gitignore 忽略，没有文件时跳过测试。
func smallestRepoPcap(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "data", "dumps", "*.pcap"))
	if err != nil || len(matches) == 0 {
		t.Skip("仓库内没有 data/dumps/*.pcap，跳过真实 pcap 回放测试")
	}
	best, bestSize := "", int64(-1)
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		if bestSize < 0 || fi.Size() < bestSize {
			best, bestSize = m, fi.Size()
		}
	}
	return best
}

// pcapTimeRange 预读 pcap，返回时间戳区间与报文数。
func pcapTimeRange(t *testing.T, path string) (first, last time.Time, n int) {
	t.Helper()
	src, err := capture.OpenPcapFile(path)
	if err != nil {
		t.Fatalf("OpenPcapFile: %v", err)
	}
	defer src.Close()
	for {
		p, err := src.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if n == 0 || p.Ts.Before(first) {
			first = p.Ts
		}
		if n == 0 || p.Ts.After(last) {
			last = p.Ts
		}
		n++
	}
	return first, last, n
}

// contains 判断收集到的日志里是否出现了指定片段。
func (c *logCapture) contains(s string) bool { return strings.Contains(c.String(), s) }
