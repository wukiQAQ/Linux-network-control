// 采集过滤装饰器：把过滤表达式套在任意数据源之上，
// 让回放 / 演示 / 真实抓包三种数据源共用同一套过滤语义（便于离线验证）。
package capture

import (
	"context"
	"sync/atomic"
)

// Matcher 由 internal/filter 实现：判断一个以太网帧是否应该被采集。
// 这里用接口而非具体类型，采集层就不必依赖过滤表达式的解析实现，测试也更容易替换。
type Matcher interface {
	Match(frame []byte) bool
}

// FilteredSource 在数据源之上加一层过滤：命中的帧照常返回，未命中的直接丢弃并计数。
type FilteredSource struct {
	inner    Source
	m        Matcher
	filtered atomic.Uint64
}

// NewFilteredSource 包装数据源；m 为 nil 时等价于不过滤。
func NewFilteredSource(src Source, m Matcher) *FilteredSource {
	return &FilteredSource{inner: src, m: m}
}

// Next 返回下一个命中过滤条件的帧；被过滤掉的帧不返回，直接丢弃并计数。
func (f *FilteredSource) Next(ctx context.Context) (*Packet, error) {
	for {
		p, err := f.inner.Next(ctx)
		if err != nil {
			return nil, err
		}
		if f.m == nil || f.m.Match(p.Raw) {
			return p, nil
		}
		f.filtered.Add(1)
	}
}

// Close 关闭底层数据源。
func (f *FilteredSource) Close() error { return f.inner.Close() }

// Stats 直接透传底层数据源的收包/丢包统计（被过滤的帧不算丢包）。
func (f *FilteredSource) Stats() Stats { return f.inner.Stats() }

// Filtered 返回因不匹配过滤条件而被丢弃的帧数。
func (f *FilteredSource) Filtered() uint64 { return f.filtered.Load() }
