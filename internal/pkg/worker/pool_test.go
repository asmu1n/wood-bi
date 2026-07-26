package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolStartReturnsIndependentHandles(t *testing.T) {
	p := New[int](1, 2)

	h1 := p.Start(context.Background())
	h2 := p.Start(context.Background())
	if h1 == nil || h2 == nil || h1 == h2 {
		t.Fatalf("want two distinct handles")
	}

	go func() {
		for range h1.GetResultQueue() {
		}
	}()
	go func() {
		for range h2.GetResultQueue() {
		}
	}()
	h1.Stop()
	h2.Stop()
}

func TestBasicSubmitAndCollect(t *testing.T) {
	h := Start[int](context.Background(), 2, 4)

	const n = 10
	for i := 0; i < n; i++ {
		i := i
		if err := h.SubmitWithIndex(func(ctx context.Context) (int, error) {
			return i * 2, nil
		}, i); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	results := h.Collect()
	if len(results) != n {
		t.Fatalf("len(results)=%d, want %d", len(results), n)
	}

	seen := map[int]bool{}
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("unexpected err: %v", r.Err)
		}
		if r.Value != r.Index*2 {
			t.Fatalf("value=%d index=%d", r.Value, r.Index)
		}
		seen[r.Index] = true
	}
	if len(seen) != n {
		t.Fatalf("seen %d unique indices", len(seen))
	}
}

func TestTrySubmitFull(t *testing.T) {
	h := Start[struct{}](context.Background(), 1, 1)
	block := make(chan struct{})
	defer func() {
		close(block)
		go func() {
			for range h.GetResultQueue() {
			}
		}()
		h.Stop()
	}()

	task := func(ctx context.Context) (struct{}, error) {
		<-block
		return struct{}{}, nil
	}

	if err := h.Submit(task); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	if err := h.TrySubmit(task); err != nil {
		t.Fatalf("second TrySubmit: %v", err)
	}
	if err := h.TrySubmit(task); !errors.Is(err, ErrFull) {
		t.Fatalf("third TrySubmit got %v, want ErrFull", err)
	}
}

func TestPanicRecovered(t *testing.T) {
	h := Start[int](context.Background(), 1, 1)
	if err := h.Submit(func(ctx context.Context) (int, error) {
		panic("boom")
	}); err != nil {
		t.Fatal(err)
	}
	results := h.Collect()
	if len(results) != 1 {
		t.Fatalf("len=%d", len(results))
	}
	if results[0].Err == nil || results[0].Err.Error() == "" {
		t.Fatalf("want panic error, got %v", results[0].Err)
	}
}

func TestInFlightResultNotDroppedOnStop(t *testing.T) {
	h := Start[string](context.Background(), 1, 1)

	started := make(chan struct{})
	if err := h.Submit(func(ctx context.Context) (string, error) {
		close(started)
		time.Sleep(50 * time.Millisecond)
		return "done", nil
	}); err != nil {
		t.Fatal(err)
	}
	<-started

	var got []Result[string]
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := range h.GetResultQueue() {
			got = append(got, r)
		}
	}()

	h.Stop()
	wg.Wait()

	if len(got) != 1 || got[0].Value != "done" || got[0].Err != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestQueuedTasksDrainedOnStop(t *testing.T) {
	h := Start[int](context.Background(), 1, 4)

	var ran atomic.Int32
	gate := make(chan struct{})

	if err := h.Submit(func(ctx context.Context) (int, error) {
		<-gate
		ran.Add(1)
		return 1, nil
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	for i := 0; i < 3; i++ {
		if err := h.Submit(func(ctx context.Context) (int, error) {
			ran.Add(1)
			return 1, nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range h.GetResultQueue() {
		}
	}()

	close(gate)
	h.Stop()
	wg.Wait()

	if ran.Load() != 4 {
		t.Fatalf("ran=%d, want 4", ran.Load())
	}
}

func TestSubmitAfterStop(t *testing.T) {
	h := Start[int](context.Background(), 1, 1)
	go func() {
		for range h.GetResultQueue() {
		}
	}()
	h.Stop()

	err := h.Submit(func(ctx context.Context) (int, error) { return 0, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestStopIdempotent(t *testing.T) {
	h := Start[int](context.Background(), 1, 1)
	go func() {
		for range h.GetResultQueue() {
		}
	}()
	h.Stop()
	h.Stop()
	h.Stop()
}

func TestParentContextCancelPropagatesToTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := Start[int](ctx, 1, 1)

	sawCancel := make(chan struct{})
	if err := h.Submit(func(taskCtx context.Context) (int, error) {
		<-taskCtx.Done()
		close(sawCancel)
		return 0, taskCtx.Err()
	}); err != nil {
		t.Fatal(err)
	}

	cancel()

	select {
	case <-sawCancel:
	case <-time.After(time.Second):
		t.Fatal("task did not observe cancel")
	}

	go func() {
		for range h.GetResultQueue() {
		}
	}()
	h.Stop()
}

func TestNilTask(t *testing.T) {
	h := Start[int](context.Background(), 1, 1)
	defer func() {
		go func() {
			for range h.GetResultQueue() {
			}
		}()
		h.Stop()
	}()
	if err := h.Submit(nil); !errors.Is(err, ErrNilTask) {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentSubmits(t *testing.T) {
	h := Start[int](context.Background(), 4, 8)

	var (
		mu      sync.Mutex
		got     []Result[int]
		drainWG sync.WaitGroup
	)
	drainWG.Add(1)
	go func() {
		defer drainWG.Done()
		for r := range h.GetResultQueue() {
			mu.Lock()
			got = append(got, r)
			mu.Unlock()
		}
	}()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			if err := h.SubmitWithIndex(func(ctx context.Context) (int, error) {
				return i, nil
			}, i); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("submit: %v", err)
	}

	h.Stop()
	drainWG.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("len=%d want %d", len(got), n)
	}
}

func TestStopUnblocksBlockedSubmit(t *testing.T) {
	h := Start[int](context.Background(), 1, 1)

	block := make(chan struct{})
	if err := h.Submit(func(ctx context.Context) (int, error) {
		<-block
		return 1, nil
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := h.Submit(func(ctx context.Context) (int, error) { return 2, nil }); err != nil {
		t.Fatal(err)
	}

	submitDone := make(chan error, 1)
	go func() {
		submitDone <- h.Submit(func(ctx context.Context) (int, error) { return 3, nil })
	}()
	time.Sleep(20 * time.Millisecond)

	go func() {
		for range h.GetResultQueue() {
		}
	}()

	stopDone := make(chan struct{})
	go func() {
		h.Stop()
		close(stopDone)
	}()

	select {
	case err := <-submitDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked submit got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Submit was not unblocked by Stop cancel")
	}

	close(block)
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish after workers released")
	}
}

func TestReusePoolConfigAfterHandleStop(t *testing.T) {
	p := New[int](2, 2)

	h1 := p.Start(context.Background())
	if err := h1.SubmitWithIndex(func(ctx context.Context) (int, error) {
		return 7, nil
	}, 1); err != nil {
		t.Fatal(err)
	}
	got1 := h1.Collect()
	if len(got1) != 1 || got1[0].Value != 7 {
		t.Fatalf("h1: %+v", got1)
	}

	// 同一 Pool 再 Start 新会话
	h2 := p.Start(context.Background())
	if err := h2.SubmitWithIndex(func(ctx context.Context) (int, error) {
		return 9, nil
	}, 2); err != nil {
		t.Fatal(err)
	}
	got2 := h2.Collect()
	if len(got2) != 1 || got2[0].Value != 9 {
		t.Fatalf("h2: %+v", got2)
	}
}

func TestDeferStopPattern(t *testing.T) {
	run := func() []Result[int] {
		h := Start[int](context.Background(), 1, 2)
		defer h.Stop()

		// 边收结果边提交
		var (
			mu  sync.Mutex
			out []Result[int]
			wg  sync.WaitGroup
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range h.GetResultQueue() {
				mu.Lock()
				out = append(out, r)
				mu.Unlock()
			}
		}()

		_ = h.Submit(func(ctx context.Context) (int, error) { return 1, nil })
		_ = h.Submit(func(ctx context.Context) (int, error) { return 2, nil })
		// defer Stop 关闭 Results，drain 结束
		// 但 defer 在 return 前执行；需要先 Stop 再 wait drain
		// 这里显式 Stop 一次（幂等），再 wait
		h.Stop()
		wg.Wait()

		mu.Lock()
		defer mu.Unlock()
		return out
	}

	out := run()
	if len(out) != 2 {
		t.Fatalf("len=%d want 2", len(out))
	}
}
