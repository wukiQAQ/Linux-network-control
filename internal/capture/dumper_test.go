package capture

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"
)

// TestPcapWriterRoundTrip 验证流式写出的 pcap 能被回放读取（即 Wireshark 可解析的格式）。
func TestPcapWriterRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dump.pcap")
	w, err := NewPcapWriter(path)
	if err != nil {
		t.Fatalf("NewPcapWriter: %v", err)
	}
	want := []*Packet{
		{Ts: time.Unix(1700000000, 500000000).UTC(), Raw: []byte{1, 2, 3, 4, 5, 6}},
		{Ts: time.Unix(1700000001, 0).UTC(), Raw: []byte{9, 9, 9}},
	}
	for _, p := range want {
		if err := w.Write(p); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Write(nil); err != nil {
		t.Fatalf("Write(nil) 应被忽略: %v", err)
	}
	if err := w.Write(&Packet{}); err != nil {
		t.Fatalf("空帧应被忽略: %v", err)
	}
	packets, bytes := w.Stats()
	if packets != 2 || bytes != 9 {
		t.Fatalf("统计错误: packets=%d bytes=%d", packets, bytes)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := OpenPcapFile(path)
	if err != nil {
		t.Fatalf("写出的文件无法回放: %v", err)
	}
	defer r.Close()
	for i, exp := range want {
		got, err := r.Next(context.Background())
		if err != nil {
			t.Fatalf("读取第 %d 帧: %v", i, err)
		}
		if string(got.Raw) != string(exp.Raw) {
			t.Errorf("第 %d 帧内容不一致: %v != %v", i, got.Raw, exp.Raw)
		}
		if !got.Ts.Equal(exp.Ts) {
			t.Errorf("第 %d 帧时间不一致: %v != %v", i, got.Ts, exp.Ts)
		}
	}
	if _, err := r.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Errorf("读完应返回 io.EOF，实际 %v", err)
	}
	if s := r.Stats(); s.Packets != 2 {
		t.Errorf("回放统计错误: %+v", s)
	}
}

// TestDumperLifecycle 覆盖开始 / 写入 / 手动停止 / 重复停止 / 参数夹取的完整流程。
func TestDumperLifecycle(t *testing.T) {
	d := NewDumper(t.TempDir())
	if _, active := d.Status(); active {
		t.Fatal("初始状态不应在导出中")
	}
	if _, err := d.Stop(); err == nil {
		t.Fatal("没有导出时 Stop 应报错")
	}

	res, err := d.Start(5)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Seconds != 5 || res.ID == "" || res.File == "" {
		t.Fatalf("Start 返回值异常: %+v", res)
	}
	if _, active := d.Status(); !active {
		t.Fatal("导出进行中时 Status 应报告 active")
	}
	if _, err := d.Start(5); err == nil {
		t.Fatal("重复 Start 应报错")
	}

	d.Write(&Packet{Ts: time.Now().UTC(), Raw: []byte{1, 2, 3, 4}})
	d.Write(nil)               // 忽略
	d.Write(&Packet{Raw: nil}) // 忽略

	cur, active := d.Status()
	if !active || cur.Packets != 1 || cur.Bytes != 4 {
		t.Fatalf("导出中的中间状态错误: active=%v %+v", active, cur)
	}

	out, err := d.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if out.Packets != 1 || out.Bytes != 4 || out.Ended.IsZero() {
		t.Fatalf("Stop 结果错误: %+v", out)
	}
	if _, active := d.Status(); active {
		t.Fatal("停止后不应再是导出中")
	}
	r, err := OpenPcapFile(out.File)
	if err != nil {
		t.Fatalf("导出的文件无法回放: %v", err)
	}
	pkt, err := r.Next(context.Background())
	if err != nil || len(pkt.Raw) != 4 {
		t.Fatalf("导出文件内容异常: %v %v", pkt, err)
	}
	r.Close()

	// 参数夹取：过小按 1 秒，过大按 60 秒
	small, err := d.Start(0)
	if err != nil {
		t.Fatalf("Start(0): %v", err)
	}
	if small.Seconds != DumpMinSeconds {
		t.Errorf("过小的秒数应夹到 %d，实际 %d", DumpMinSeconds, small.Seconds)
	}
	if _, err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	big, err := d.Start(9999)
	if err != nil {
		t.Fatalf("Start(9999): %v", err)
	}
	if big.Seconds != DumpMaxSeconds {
		t.Errorf("过大的秒数应夹到 %d，实际 %d", DumpMaxSeconds, big.Seconds)
	}
	if _, err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestDumperAutoStop 验证到达设定时长后定时器会自动结束导出。
func TestDumperAutoStop(t *testing.T) {
	d := NewDumper(t.TempDir())
	if _, err := d.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	d.Write(&Packet{Ts: time.Now().UTC(), Raw: []byte{1, 2, 3}})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if res, active := d.Status(); !active {
			if res.Packets != 1 {
				t.Fatalf("自动停止后统计错误: %+v", res)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("到达时长后未自动停止导出")
}
