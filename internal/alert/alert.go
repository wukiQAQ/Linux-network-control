// Package alert 实现阈值告警引擎：消费周期指标快照，按"持续超过阈值 N 秒"
// 状态机触发告警与恢复，保留事件历史并支持 Webhook 通知。
package alert

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Rule 一条告警规则：某指标持续超过阈值 for 时长后触发。
type Rule struct {
	ID        string
	Series    string // 指标名，如 traffic.bps
	Threshold float64
	For       time.Duration
}

// Event 一条告警事件（触发或恢复都会记录一条）。
type Event struct {
	RuleID     string     `json:"rule_id"`
	Series     string     `json:"series"`
	Value      float64    `json:"value"`
	Threshold  float64    `json:"threshold"`
	Status     string     `json:"status"` // firing | resolved
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	Notified   bool       `json:"notified"`
}

// Point 一次评估中的一个指标值。
type Point struct {
	Series string
	Value  float64
}

// Options 引擎可选配置。
type Options struct {
	Webhook   string            // 触发时通知的 URL，空则不通知
	MaxEvents int               // 事件历史上限（默认 200）
	Notify    func(Event) error // 通知函数（测试注入用），nil 表示用默认 Webhook 实现
}

// Engine 是并发安全的告警状态机。
type Engine struct {
	mu      sync.Mutex
	rules   []Rule
	webhook string
	maxEvt  int
	notify  func(Event) error
	since   map[string]time.Time
	firing  map[string]Event
	events  []Event
	client  *http.Client
}

// New 创建引擎；webhook 为空表示不通知。
func New(rules []Rule, webhook string) *Engine {
	return NewWithOptions(rules, Options{Webhook: webhook})
}

func NewWithOptions(rules []Rule, opts Options) *Engine {
	max := opts.MaxEvents
	if max <= 0 {
		max = 200
	}
	return &Engine{
		rules:   append([]Rule(nil), rules...),
		webhook: opts.Webhook,
		maxEvt:  max,
		notify:  opts.Notify,
		since:   map[string]time.Time{},
		firing:  map[string]Event{},
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (e *Engine) valueOf(pts []Point, series string) (float64, bool) {
	for _, p := range pts {
		if p.Series == series {
			return p.Value, true
		}
	}
	return 0, false
}

// Evaluate 用一次指标快照推进状态机，返回本次产生的新事件（触发/恢复）。
func (e *Engine) Evaluate(now time.Time, pts []Point) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Event
	for _, r := range e.rules {
		v, ok := e.valueOf(pts, r.Series)
		over := ok && v > r.Threshold
		key := r.ID
		if over {
			if _, has := e.since[key]; !has {
				e.since[key] = now
			}
			if _, firing := e.firing[key]; !firing && now.Sub(e.since[key]) >= r.For {
				ev := Event{
					RuleID: r.ID, Series: r.Series, Value: v, Threshold: r.Threshold,
					Status: "firing", StartedAt: now,
				}
				ev.Notified = e.notifyEvent(ev)
				e.firing[key] = ev
				e.push(ev)
				out = append(out, ev)
			} else if cur, firing := e.firing[key]; firing {
				cur.Value = v
				e.firing[key] = cur
			}
		} else {
			delete(e.since, key)
			if cur, firing := e.firing[key]; firing {
				cur.Status = "resolved"
				t := now
				cur.ResolvedAt = &t
				cur.Value = v
				delete(e.firing, key)
				e.push(cur)
				out = append(out, cur)
			}
		}
	}
	return out
}

// List 返回事件历史副本（按时间倒序），可选按状态过滤。
func (e *Engine) List(status string) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Event, 0, len(e.events))
	for _, ev := range e.events {
		if status == "" || status == ev.Status {
			out = append(out, ev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Firing 返回当前处于触发中的事件（来自状态机激活集合，而非事件历史）。
func (e *Engine) Firing() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Event, 0, len(e.firing))
	for _, ev := range e.firing {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func (e *Engine) push(ev Event) {
	e.events = append(e.events, ev)
	if len(e.events) > e.maxEvt {
		e.events = e.events[len(e.events)-e.maxEvt:]
	}
}

// notifyEvent 触发时通知一次；失败只记日志不阻塞状态机。
func (e *Engine) notifyEvent(ev Event) bool {
	if e.notify != nil {
		return e.notify(ev) == nil
	}
	if e.webhook == "" {
		return false
	}
	body, err := json.Marshal(ev)
	if err != nil {
		log.Printf("[alert] 通知序列化失败: %v", err)
		return false
	}
	resp, err := e.client.Post(e.webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[alert] Webhook 通知失败 %s: %v", ev.RuleID, err)
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
