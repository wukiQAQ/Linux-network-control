package aggregator

import (
	"testing"
	"time"
)

func TestTickRates(t *testing.T) {
	t0 := time.Unix(1700000000, 0).UTC()
	a := New(100)
	a.Tick(t0, 5, 0) // 首帧只建立基准
	a.Observe(1500)
	a.Observe(1500)
	a.RecordDrop()
	s := a.Tick(t0.Add(time.Second), 6, 1)
	if s.Bps != 24000 { // 3000 字节 * 8 bit / 1s
		t.Errorf("Bps=%v, want 24000", s.Bps)
	}
	if s.Pps != 2 {
		t.Errorf("Pps=%v, want 2", s.Pps)
	}
	if s.Active != 6 || s.NewConns != 1 || s.DropEvents != 1 {
		t.Errorf("采样字段错误: %+v", s)
	}
	if s.Ts != t0.Add(time.Second) {
		t.Errorf("采样时间错误: %v", s.Ts)
	}
}

func TestRingCapacity(t *testing.T) {
	a := New(5)
	base := time.Unix(1700000000, 0).UTC()
	for i := 0; i < 20; i++ {
		a.Tick(base.Add(time.Duration(i)*time.Second), i, uint64(i))
	}
	if got := len(a.History()); got != 5 {
		t.Errorf("环形缓冲长度=%d, want 5", got)
	}
	if got := a.Last().Active; got != 19 {
		t.Errorf("Last().Active=%d, want 19", got)
	}
}
