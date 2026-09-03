// pcap 文件读写：回放模式的数据基础。
// 实现为极简 pcap 经典格式（24 字节全局头 + 16 字节记录头），
// 支持微秒/纳秒时间戳与大小端，链路类型仅支持以太网。
package capture

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	magicMicroLE = 0xa1b2c3d4 // 标准微秒时间戳（小端）
	magicNanoLE  = 0xa1b23c4d // 纳秒时间戳（小端）
	linkEthernet = 1
	maxFrameLen  = 1 << 20 // 防御异常文件：单帧上限 1MB
)

// PcapFile 顺序读取 pcap 文件中的报文。
type PcapFile struct {
	r     *bufio.Reader
	f     *os.File
	end   binary.ByteOrder
	nano  bool
	pkts  uint64
	drops uint64
}

// OpenPcapFile 打开 pcap 文件并校验全局头。
func OpenPcapFile(path string) (*PcapFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, 24)
	if _, err := io.ReadFull(f, hdr); err != nil {
		f.Close()
		return nil, fmt.Errorf("读取 pcap 全局头失败: %w", err)
	}
	var end binary.ByteOrder
	var nano bool
	switch binary.LittleEndian.Uint32(hdr[0:4]) {
	case magicMicroLE:
		end, nano = binary.LittleEndian, false
	case magicNanoLE:
		end, nano = binary.LittleEndian, true
	case 0xd4c3b2a1: // 大端微秒
		end, nano = binary.BigEndian, false
	case 0x4d3cb2a1: // 大端纳秒
		end, nano = binary.BigEndian, true
	default:
		f.Close()
		return nil, errors.New("无法识别的 pcap 文件头")
	}
	link := end.Uint16(hdr[20:22])
	if link != linkEthernet {
		f.Close()
		return nil, fmt.Errorf("暂不支持链路类型 %d（仅支持以太网）", link)
	}
	return &PcapFile{r: bufio.NewReader(f), f: f, end: end, nano: nano}, nil
}

// Next 读取下一帧；文件读完返回 io.EOF。
func (p *PcapFile) Next(ctx context.Context) (*Packet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rh := make([]byte, 16)
	if _, err := io.ReadFull(p.r, rh); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, io.EOF
		}
		return nil, err
	}
	sec := p.end.Uint32(rh[0:4])
	frac := p.end.Uint32(rh[4:8])
	incl := p.end.Uint32(rh[8:12])
	if incl > maxFrameLen {
		p.drops++
		return nil, fmt.Errorf("帧长度异常: %d", incl)
	}
	raw := make([]byte, incl)
	if _, err := io.ReadFull(p.r, raw); err != nil {
		return nil, err
	}
	var ts time.Time
	if p.nano {
		ts = time.Unix(int64(sec), int64(frac)).UTC()
	} else {
		ts = time.Unix(int64(sec), int64(frac)*1000).UTC()
	}
	p.pkts++
	return &Packet{Ts: ts, Raw: raw, Iface: "replay"}, nil
}

func (p *PcapFile) Close() error      { return p.f.Close() }
func (p *PcapFile) Stats() Stats      { return Stats{Packets: p.pkts, Drops: p.drops} }

// WritePCAP 将报文写入 pcap 文件（小端、微秒、以太网）。供测试与离线回放样本生成。
func WritePCAP(path string, pkts []Packet) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint32(hdr[0:4], magicMicroLE)
	binary.LittleEndian.PutUint16(hdr[4:6], 2) // 版本 2.4
	binary.LittleEndian.PutUint16(hdr[6:8], 4)
	binary.LittleEndian.PutUint32(hdr[16:20], 65535) // snaplen
	binary.LittleEndian.PutUint32(hdr[20:24], linkEthernet)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for _, p := range pkts {
		rh := make([]byte, 16)
		sec := uint32(p.Ts.Unix())
		frac := uint32(p.Ts.Nanosecond() / 1000)
		binary.LittleEndian.PutUint32(rh[0:4], sec)
		binary.LittleEndian.PutUint32(rh[4:8], frac)
		binary.LittleEndian.PutUint32(rh[8:12], uint32(len(p.Raw)))
		binary.LittleEndian.PutUint32(rh[12:16], uint32(len(p.Raw)))
		if _, err := w.Write(rh); err != nil {
			return err
		}
		if _, err := w.Write(p.Raw); err != nil {
			return err
		}
	}
	return w.Flush()
}
