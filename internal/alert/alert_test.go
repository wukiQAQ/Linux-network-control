package alert

import (
	"sync"
	"testing"
	"time"
)

func TestFiresAfterForDurationAndResolves(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	eng := NewWithOptions([]Rule{
		{ID: "bps-high", Series: "traffic.bps", Threshold: 1000, For: 2 * time.Second},
	}, Options{MaxEvents: 20, Notify: func(Event) error { return nil }})

	pts := []Point{{Series: "traffic.bps", Value: 2000}}
	// 前两次未达持续时长：不触发
	if got := eng.Evaluate(base, pts); len(got) != 0 {
		t.Fatalf("持续未满即触发: %+v", got)
	}
	if got := eng.Evaluate(base.Add(1*time.Second), pts); len(got) != 0 {
		t.Fatalf("持续未满即触发: %+v", got)
	}
	// 第三次达到 2 秒：触发
	got := eng.Evaluate(base.Add(2*time.Second), pts)
	if len(got) != 1 || got[0].Status != "firing" || got[0].Series != "traffic.bps" {
		t.Fatalf("触发事件异常: %+v", got)
	}
	if len(eng.Firing()) != 1 {
		t.Fatalf("Firing 数=%d, want 1", len(eng.Firing()))
	}
	// 指标回落：恢复
	low := []Point{{Series: "traffic.bps", Value: 100}}
	got = eng.Evaluate(base.Add(3*time.Second), low)
	if len(got) != 1 || got[0].Status != "resolved" || got[0].ResolvedAt.IsZero() {
		t.Fatalf("恢复事件异常: %+v", got)
	}
	if len(eng.Firing()) != 0 {
		t.Fatal("恢复后仍处于触发状态")
	}
}

func TestTriggerNotifiesOncePerIncident(t *testing.T) {
	var mu sync.Mutex
	notified := 0
	eng := NewWithOptions([]Rule{
		{ID: "pps-high", Series: "traffic.pps", Threshold: 500, For: time.Second},
	}, Options{Notify: func(ev Event) error {
		if ev.Status != "firing" {
			t.Errorf("通知状态=%s, want firing", ev.Status)
		}
		mu.Lock()
		notified++
		mu.Unlock()
		return nil
	}})
	base := time.Unix(1700000100, 0).UTC()
	pts := []Point{{Series: "traffic.pps", Value: 800}}
	eng.Evaluate(base, pts)
	eng.Evaluate(base.Add(time.Second), pts) // 触发并通知一次
	eng.Evaluate(base.Add(2*time.Second), pts)
	eng.Evaluate(base.Add(3*time.Second), pts) // 仍在触发：不重复通知
	mu.Lock()
	if notified != 1 {
		t.Fatalf("通知次数=%d, want 1", notified)
	}
	mu.Unlock()
}

func TestMissingMetricResolvesAndHistoryOrder(t *testing.T) {
	base := time.Unix(1700000200, 0).UTC()
	eng := NewWithOptions([]Rule{
		{ID: "conns-high", Series: "traffic.conns", Threshold: 100, For: 0},
	}, Options{MaxEvents: 5, Notify: func(Event) error { return nil }})
	eng.Evaluate(base, []Point{{Series: "traffic.conns", Value: 200}})
	eng.Evaluate(base.Add(time.Second), []Point{{Series: "traffic.conns", Value: 10}})
	if len(eng.Firing()) != 0 {
		t.Fatal("指标缺失后仍处于触发状态")
	}
	list := eng.List("")
	if len(list) != 2 {
		t.Fatalf("事件数=%d, want 2", len(list))
	}
	foundResolved := false
	for _, ev := range list {
		if ev.Status == "resolved" {
			foundResolved = true
		}
	}
	if !foundResolved {
		t.Error("事件历史缺少恢复记录")
	}
	// 历史环上限
	for i := 0; i < 10; i++ {
		eng.Evaluate(base.Add(time.Duration(i+2)*time.Second), []Point{{Series: "traffic.conns", Value: 300}})
		eng.Evaluate(base.Add(time.Duration(i+2)*time.Second+time.Millisecond), []Point{{Series: "traffic.conns", Value: 1}})
	}
	if n := len(eng.List("")); n > 5 {
		t.Fatalf("事件数=%d 超出上限 5", n)
	}
}
