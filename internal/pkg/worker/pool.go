// 用法：
//
//	h := worker.Start[int](ctx, 4, 64)
//	defer h.Stop()
//	_ = h.Submit(fn)
//	for r := range h.Results() { ... }
//
// 批处理可直接 h.Collect()（内部 Stop 并排干 Results）。
//
// 若无人读取 Results 且缓冲被填满，worker/Stop 可能阻塞；常驻场景请另路持续读取。
package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrFull TrySubmit 时任务队列已满。
	ErrFull = errors.New("worker: task queue full")
)

type task[T any] struct {
	fn    func(ctx context.Context) (T, error)
	index int
}

// Result 单条任务的执行结果。
type Result[T any] struct {
	Value T
	Err   error
	Index int
}

// PoolProto 工作池配置（工厂）。无运行状态，可多次 Start 得到彼此独立的 Handle。
type PoolProto[T any] struct {
	workerCount int
	queueSize   int
}

// Pool 一次运行中的工作池会话。由 Pool.Start / worker.Start 创建。
type Pool[T any] struct {
	workerCount int

	taskQueue   chan task[T]
	resultQueue chan Result[T]

	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once
}

// New 创建配置。workerCount<=0 回落为 1；queueSize<0 回落为 0。
func New[T any](workerCount, queueSize int) *PoolProto[T] {
	if workerCount <= 0 {
		workerCount = 1
	}
	if queueSize < 0 {
		queueSize = 0
	}
	return &PoolProto[T]{
		workerCount: workerCount,
		queueSize:   queueSize,
	}
}

// Start 按配置启动一次运行会话，返回 Handle。
// ctx 会派生为会话上下文并传入任务 fn；每次 Start 都是新会话，互不影响。
func (p *PoolProto[T]) Start(ctx context.Context) *Pool[T] {
	if p == nil {
		p = New[T](1, 0)
	}
	return startHandle[T](ctx, p.workerCount, p.queueSize)
}

// Start 便捷方法：等价于 New[T](workerCount, queueSize).Start(ctx)。
func Start[T any](ctx context.Context, workerCount, queueSize int) *Pool[T] {
	return New[T](workerCount, queueSize).Start(ctx)
}

func startHandle[T any](ctx context.Context, workerCount, queueSize int) *Pool[T] {
	if ctx == nil {
		ctx = context.Background()
	}
	if workerCount <= 0 {
		workerCount = 1
	}
	if queueSize < 0 {
		queueSize = 0
	}

	ctx, cancel := context.WithCancel(ctx)

	h := &Pool[T]{
		workerCount: workerCount,
		taskQueue:   make(chan task[T], queueSize),
		// 略大于 task 队列，降低排空时的阻塞概率。
		resultQueue: make(chan Result[T], queueSize+workerCount),
		ctx:         ctx,
		cancel:      cancel,
	}

	for i := 0; i < workerCount; i++ {
		h.wg.Add(1)
		go h.worker()
	}
	return h
}

// Submit 阻塞提交任务，直到入队或会话上下文取消。
func (h *Pool[T]) Submit(fn func(ctx context.Context) (T, error)) error {
	return h.SubmitWithIndex(fn, -1)
}

// SubmitWithIndex 同 Submit，并带上 Index。
func (h *Pool[T]) SubmitWithIndex(fn func(ctx context.Context) (T, error), index int) error {
	return h.push(fn, index, false)
}

// TrySubmit 非阻塞提交；队列满时返回 ErrFull。
func (h *Pool[T]) TrySubmit(fn func(ctx context.Context) (T, error)) error {
	return h.TrySubmitWithIndex(fn, -1)
}

// TrySubmitWithIndex 非阻塞提交并带 Index。
func (h *Pool[T]) TrySubmitWithIndex(fn func(ctx context.Context) (T, error), index int) error {
	return h.push(fn, index, true)
}

// push 将任务送入队列。
// 无 mutex：ctx/taskQueue 创建后只读；关闭与发送的竞态靠 ctx 取消 + recover。
func (h *Pool[T]) push(fn func(ctx context.Context) (T, error), index int, try bool) (err error) {
	// Stop 先 cancel 再 close：正常路径走 ctx.Done()。
	// 极端竞态下可能 send 到已 close 的 channel，recover 后回落为 ctx.Err()。
	defer func() {
		if r := recover(); r != nil {
			if e := h.ctx.Err(); e != nil {
				err = e
				return
			}
			err = fmt.Errorf("worker: submit on closed queue: %v", r)
		}
	}()

	t := task[T]{fn: fn, index: index}

	if try {
		select {
		case <-h.ctx.Done():
			return h.ctx.Err()
		case h.taskQueue <- t:
			return nil
		default:
			return ErrFull
		}
	} else {
		select {
		case <-h.ctx.Done():
			return h.ctx.Err()
		case h.taskQueue <- t:
			return nil
		}
	}
}

// GetResultQueue 返回结果通道（只读）。Stop 完成后该通道会被关闭。
func (h *Pool[T]) GetResultQueue() <-chan Result[T] {
	return h.resultQueue
}

// Stop 停止接任务、等待 worker 结束并关闭结果通道。可重复调用、可作 defer。
// 已入队任务仍会执行并产出 Result；运行中的 fn 会收到会话 ctx 取消。
func (h *Pool[T]) Stop() {
	h.stopOnce.Do(func() {
		// 1) 先 cancel：唤醒堵在满队列上的 Submit，并让运行中的任务可感知取消。
		h.cancel()
		// 2) 再关任务队列：worker range 排空已入队任务。
		close(h.taskQueue)
		// 3) 等 worker 退出（每条任务仍会投递 Result）。
		h.wg.Wait()
		// 4) 关闭结果通道。
		close(h.resultQueue)
	})
}

// Collect 停止会话并收集全部结果（适合一次性批处理）。
func (h *Pool[T]) Collect() []Result[T] {
	results := make([]Result[T], 0)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for r := range h.GetResultQueue() {
			results = append(results, r)
		}
	}()

	h.Stop()
	<-done
	return results
}

func (h *Pool[T]) worker() {
	defer h.wg.Done()

	for t := range h.taskQueue {
		var val T
		var err error

		// 执行业务回调，同时兜底捕获 panic
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("worker: panic: %v", r)
				}
			}()
			val, err = t.fn(h.ctx)
		}()

		// 必须投递结果，不能因 ctx 取消而丢弃。
		h.resultQueue <- Result[T]{Value: val, Err: err, Index: t.index}
	}
}
