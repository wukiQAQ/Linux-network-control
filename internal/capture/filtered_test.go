package capture

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"testing"
	"time"

	"github.com/wukiQAQ/Linux-network-control/internal/filter"
	"github.com/wukiQAQ/Linux-network-control/internal/parser"
)

// fakeSource 按顺序吐帧，用于单独验证过滤装饰器（不依赖真实网卡）。
type fakeSource struct {
	frames [][]byte
	i      int
	closed bool
}

func (f *fakeSource) Next(ctx context.Context) (*Packet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.i >= len(f.frames) {
		return nil, io.EOF
	}
	raw := f.frames[f.i]
	f.i++
	return &Packet{Ts: time.Unix(1700000000, 0).UTC(), Raw: raw, Iface: "fake"}, nil
}

func (f *fakeSource) Close() error { f.closed = true; return nil }
func (f *fakeSource) Stats() Stats { return Stats{Packets: uint64(f.i)} }

func tcpFrame(sport, dport uint16) []byte {
	return parser.BuildEthIPv4TCP(netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("10.0.0.9"), sport, dport, []byte("x"), false)
}

func udpFrame(sport, dport uint16) []byte {
	return parser.BuildEthIPv4UDP(netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("10.0.0.9"), sport, dport, []byte("x"))
}

// TestFilteredSourceDropsNonMatching：只放行命中的帧，并单独统计被过滤掉的帧数。
func TestFilteredSourceDropsNonMatching(t *testing.T) {
	src := &fakeSource{frames: [][]byte{tcpFrame(51000, 443), udpFrame(40000, 53), tcpFrame(51001, 80)}}
	flt, err := filter.Parse("tcp and dst port 443")
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFilteredSource(src, flt)
	ctx := context.Background()
	p, err := fs.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(p.Raw) != len(src.frames[0]) {
		t.Error("返回的应该是第一条 TCP/443 帧")
	}
	if _, err := fs.Next(ctx); err != io.EOF {
		t.Errorf("过滤后应读到 EOF，实际 %v", err)
	}
	if fs.Filtered() != 2 {
		t.Errorf("Filtered() = %d, want 2", fs.Filtered())
	}
	if fs.Stats().Packets != 3 {
		t.Errorf("Stats 应透传底层数据源：Packets = %d, want 3", fs.Stats().Packets)
	}
	if err := fs.Close(); err != nil || !src.closed {
		t.Error("Close 应透传到底层数据源")
	}
}

// TestFilteredSourceNilMatcher：没有过滤器时不丢任何帧。
func TestFilteredSourceNilMatcher(t *testing.T) {
	src := &fakeSource{frames: [][]byte{tcpFrame(1, 2), udpFrame(3, 4)}}
	fs := NewFilteredSource(src, nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := fs.Next(ctx); err != nil {
			t.Fatalf("第 %d 帧: %v", i+1, err)
		}
	}
	if fs.Filtered() != 0 {
		t.Errorf("Filtered() = %d, want 0", fs.Filtered())
	}
}

// TestFilteredSourceContextCancel：取消上下文时立即返回错误，不继续空转。
func TestFilteredSourceContextCancel(t *testing.T) {
	src := &fakeSource{frames: [][]byte{tcpFrame(1, 2)}}
	flt, err := filter.Parse("udp")
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFilteredSource(src, flt)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fs.Next(ctx); err == nil {
		t.Error("期望返回取消错误")
	}
}

// TestFilteredSourceReplay：与回放数据源串联（离线即可验证过滤行为）。
func TestFilteredSourceReplay(t *testing.T) {
	path := t.TempDir() + "/f.pcap"
	pkts := []Packet{
		{Ts: time.Unix(1700000000, 0).UTC(), Raw: tcpFrame(51000, 443)},
		{Ts: time.Unix(1700000001, 0).UTC(), Raw: udpFrame(40000, 53)},
	}
	if err := WritePCAP(path, pkts); err != nil {
		t.Fatalf("WritePCAP: %v", err)
	}
	replay, err := OpenPcapFile(path)
	if err != nil {
		t.Fatalf("OpenPcapFile: %v", err)
	}
	defer replay.Close()
	flt, err := filter.Parse("udp")
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFilteredSource(replay, flt)
	p, err := fs.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(p.Raw) != len(pkts[1].Raw) {
		t.Error("应该只放行 UDP/53 帧")
	}
	if fs.Filtered() != 1 {
		t.Errorf("Filtered() = %d, want 1", fs.Filtered())
	}
	if _, err := fs.Next(context.Background()); err != io.EOF {
		t.Errorf("期望 EOF，实际 %v", err)
	}
}

// fakeKernelSource 模拟 Linux 的 Live：记录收到的内核程序，可模拟挂载失败。
type fakeKernelSource struct {
	fakeSource
	attached filter.Program
	err      error
}

func (f *fakeKernelSource) AttachKernelFilter(prog filter.Program) error {
	if f.err != nil {
		return f.err
	}
	f.attached = prog
	return nil
}

// TestFilteredSourceAttachesKernelProgram 校验：支持内核下沉时挂上程序，且过滤行为仍由用户态决定。
func TestFilteredSourceAttachesKernelProgram(t *testing.T) {
	src := &fakeKernelSource{fakeSource: fakeSource{frames: [][]byte{tcpFrame(51000, 443), udpFrame(40000, 53)}}}
	flt, err := filter.Parse("tcp and host 10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFilteredSource(src, flt)
	if !fs.KernelAttached() {
		t.Fatal("应启用内核剪枝")
	}
	if len(src.attached) == 0 {
		t.Fatal("应把保守超集程序下发给数据源")
	}
	ctx := context.Background()
	p, err := fs.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(p.Raw) != len(src.frames[0]) {
		t.Error("应放行 TCP/443 帧")
	}
	if _, err := fs.Next(ctx); err != io.EOF {
		t.Errorf("期望 EOF，实际 %v", err)
	}
	if fs.Filtered() != 1 {
		t.Errorf("用户态仍应过滤掉 UDP 帧：Filtered()=%d, want 1", fs.Filtered())
	}
}

// TestFilteredSourceKernelAttachFailure 校验：内核挂载失败只影响剪枝，不影响过滤与采集。
func TestFilteredSourceKernelAttachFailure(t *testing.T) {
	src := &fakeKernelSource{
		fakeSource: fakeSource{frames: [][]byte{tcpFrame(1, 443)}},
		err:        errors.New("operation not permitted"),
	}
	flt, err := filter.Parse("tcp")
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFilteredSource(src, flt)
	if fs.KernelAttached() {
		t.Error("挂载失败时不应标记为已启用")
	}
	if _, err := fs.Next(context.Background()); err != nil {
		t.Fatalf("挂载失败不应影响采集: %v", err)
	}
}

// TestFilteredSourceNoPruningNoAttach 校验：无法保守表达（not 子表达式）时不下发内核程序。
func TestFilteredSourceNoPruningNoAttach(t *testing.T) {
	src := &fakeKernelSource{fakeSource: fakeSource{frames: [][]byte{tcpFrame(1, 2)}}}
	flt, err := filter.Parse("not tcp")
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFilteredSource(src, flt)
	if src.attached != nil || fs.KernelAttached() {
		t.Error("not 表达式没有剪枝价值，不应下发内核程序")
	}
}

// TestFilteredSourceNoKernelSupport 校验：数据源不支持内核下沉（回放 / 演示）时静默跳过。
func TestFilteredSourceNoKernelSupport(t *testing.T) {
	src := &fakeSource{frames: [][]byte{tcpFrame(1, 443)}}
	flt, err := filter.Parse("tcp")
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFilteredSource(src, flt)
	if fs.KernelAttached() {
		t.Error("数据源不支持内核下沉时不应标记为已启用")
	}
	if _, err := fs.Next(context.Background()); err != nil {
		t.Fatalf("应照常返回帧: %v", err)
	}
}
