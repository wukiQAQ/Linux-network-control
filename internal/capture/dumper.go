// 按需抓包：把流经管道的原始报文流式写成 pcap 文件，供 Wireshark 分析。
// 与回放（pcapfile.go）共用同一种文件格式：小端、微秒时间戳、以太网链路。
package capture

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PcapWriter 以流式方式写出 pcap 文件。
type PcapWriter struct {
	mu      sync.Mutex
	f       *os.File
	w       *bufio.Writer
	packets uint64
	bytes   uint64
}

// NewPcapWriter 创建文件并写入 24 字节 pcap 全局头。
func NewPcapWriter(path string) (*PcapWriter, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	p := &PcapWriter{f: f, w: bufio.NewWriterSize(f, 1<<16)}
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint32(hdr[0:4], magicMicroLE)
	binary.LittleEndian.PutUint16(hdr[4:6], 2) // 版本 2.4
	binary.LittleEndian.PutUint16(hdr[6:8], 4)
	binary.LittleEndian.PutUint32(hdr[16:20], 65535) // snaplen
	binary.LittleEndian.PutUint32(hdr[20:24], linkEthernet)
	if _, err := p.w.Write(hdr); err != nil {
		f.Close()
		return nil, err
	}
	return p, nil
}

// Write 追加一帧原始报文；空帧被忽略。
func (p *PcapWriter) Write(pkt *Packet) error {
	if pkt == nil || len(pkt.Raw) == 0 {
		return nil
	}
	ts := pkt.Ts
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	rh := make([]byte, 16)
	binary.LittleEndian.PutUint32(rh[0:4], uint32(ts.Unix()))
	binary.LittleEndian.PutUint32(rh[4:8], uint32(ts.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(rh[8:12], uint32(len(pkt.Raw)))
	binary.LittleEndian.PutUint32(rh[12:16], uint32(len(pkt.Raw)))

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.w.Write(rh); err != nil {
		return err
	}
	if _, err := p.w.Write(pkt.Raw); err != nil {
		return err
	}
	p.packets++
	p.bytes += uint64(len(pkt.Raw))
	return nil
}

// Stats 返回已写入的包数与负载字节数。
func (p *PcapWriter) Stats() (uint64, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.packets, p.bytes
}

// Close 刷盘并关闭文件。
func (p *PcapWriter) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.w.Flush()
	if cerr := p.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// DumpResult 描述一次按需抓包的结果。
type DumpResult struct {
	ID      string
	File    string
	Seconds int
	Packets uint64
	Bytes   uint64
	Started time.Time
	Ended   time.Time
}

// Dumper 管理「导出 N 秒抓包」：同一时刻只允许一次导出，
// 时间到自动停止并保留文件，供客户端下载后用 Wireshark 打开。
type Dumper struct {
	mu     sync.Mutex
	dir    string
	writer *PcapWriter
	cur    DumpResult
	active bool
	timer  *time.Timer
	now    func() time.Time
}

// 导出时长限制（秒）：太短抓不到内容，太长会占用磁盘。
const (
	DumpMinSeconds = 1
	DumpMaxSeconds = 60
)

// NewDumper 创建抓包器，dir 为 pcap 存放目录（不存在会自动创建）。
func NewDumper(dir string) *Dumper {
	return &Dumper{dir: dir, now: time.Now}
}

// Start 开始一次导出；若已有导出在进行中则返回错误。
func (d *Dumper) Start(seconds int) (DumpResult, error) {
	if seconds < DumpMinSeconds {
		seconds = DumpMinSeconds
	}
	if seconds > DumpMaxSeconds {
		seconds = DumpMaxSeconds
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active {
		return DumpResult{}, errors.New("已有导出正在进行中")
	}
	id := fmt.Sprintf("capture-%d", d.now().UTC().UnixNano())
	file := filepath.Join(d.dir, id+".pcap")
	w, err := NewPcapWriter(file)
	if err != nil {
		return DumpResult{}, err
	}
	d.writer = w
	d.active = true
	d.cur = DumpResult{ID: id, File: file, Seconds: seconds, Started: d.now().UTC()}
	d.timer = time.AfterFunc(time.Duration(seconds)*time.Second, func() { _, _ = d.Stop() })
	return d.cur, nil
}

// Write 写入一帧；没有进行中的导出时直接忽略。
func (d *Dumper) Write(pkt *Packet) {
	d.mu.Lock()
	w := d.writer
	if w == nil {
		d.mu.Unlock()
		return
	}
	err := w.Write(pkt)
	d.mu.Unlock()
	if err != nil {
		// 写盘失败不应影响采集主流程，仅丢弃该帧。
		return
	}
}

// Stop 立即结束当前导出并落盘。
func (d *Dumper) Stop() (DumpResult, error) {
	d.mu.Lock()
	w := d.writer
	if w == nil {
		d.mu.Unlock()
		return DumpResult{}, errors.New("当前没有进行中的导出")
	}
	d.writer = nil
	d.active = false
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.mu.Unlock()

	packets, bytes := w.Stats()
	err := w.Close()

	d.mu.Lock()
	d.cur.Packets, d.cur.Bytes, d.cur.Ended = packets, bytes, d.now().UTC()
	res := d.cur
	d.mu.Unlock()
	return res, err
}

// Status 返回最近一次导出结果，以及是否仍在进行中。
func (d *Dumper) Status() (DumpResult, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	res := d.cur
	if d.active && d.writer != nil {
		res.Packets, res.Bytes = d.writer.Stats()
	}
	return res, d.active
}
