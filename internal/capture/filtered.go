// 采集过滤装饰器：把过滤表达式套在任意数据源之上，
// 让回放 / 演示 / 真实抓包三种数据源共用同一套过滤语义（便于离线验证）。
package capture

import (
	"context"
	"log"
	"sync/atomic"

	"github.com/wukiQAQ/Linux-network-control/internal/filter"
)

// Matcher 由 internal/filter 实现：判断一个以太网帧是否应该被采集。
// 这里用接口而非具体类型，采集层就不必依赖过滤表达式的解析实现，测试也更容易替换。
type Matcher interface {
	Match(frame []byte) bool
}

// kernelAttacher 由 Linux 的 Live 数据源实现：把过滤程序下沉到内核，
// 让明显不匹配的帧在内核态就被丢掉，省掉用户态拷贝。
type kernelAttacher interface {
	AttachKernelFilter(prog filter.Program) error
}

// kernelProgrammer 由过滤表达式实现：给出可下沉内核的**保守超集**程序
// （超集保证"用户态会命中的帧，内核一定放行"，因此绝不会误丢需要采集的流量）。
type kernelProgrammer interface {
	KernelProgram() (filter.Program, bool)
}

// FilteredSource 在数据源之上加一层过滤：命中的帧照常返回，未命中的直接丢弃并计数。
type FilteredSource struct {
	inner    Source
	m        Matcher
	filtered atomic.Uint64
	kernel   atomic.Bool
}

// NewFilteredSource 包装数据源；m 为 nil 时等价于不过滤。
// 若数据源与过滤器都支持，会顺手把"保守超集程序"下沉到内核（Linux SO_ATTACH_FILTER）；
// 下沉失败只记录日志，用户态过滤照旧生效，不影响正确性。
func NewFilteredSource(src Source, m Matcher) *FilteredSource {
	f := &FilteredSource{inner: src, m: m}
	f.tryAttachKernel()
	return f
}

// tryAttachKernel 尝试把过滤程序下沉到内核。程序是用户态条件的超集，
// 因此内核只会"少放一部分明显无关的流量进来"，精确判定始终由 Match 完成。
func (f *FilteredSource) tryAttachKernel() {
	kp, ok := f.m.(kernelProgrammer)
	if !ok {
		return
	}
	prog, ok := kp.KernelProgram()
	if !ok {
		return // 无法保守表达（例如 not 子表达式）：只用用户态过滤
	}
	att, ok := f.inner.(kernelAttacher)
	if !ok {
		return
	}
	if err := att.AttachKernelFilter(prog); err != nil {
		log.Printf("[filter] 内核剪枝未启用（继续用用户态过滤）: %v", err)
		return
	}
	f.kernel.Store(true)
	log.Printf("[filter] 内核剪枝已启用（%d 条 BPF 指令；程序为超集，精确匹配仍在用户态）", len(prog))
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

// Filtered 返回因不匹配过滤条件而被用户态丢弃的帧数。
// 注意：启用内核剪枝后，一部分帧在内核态就被丢掉、不会到达这里，因此该计数会比"全部在用户态过滤"时小。
func (f *FilteredSource) Filtered() uint64 { return f.filtered.Load() }

// KernelAttached 表示是否成功启用了内核剪枝（SO_ATTACH_FILTER）。
func (f *FilteredSource) KernelAttached() bool { return f.kernel.Load() }
