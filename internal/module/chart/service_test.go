package chart

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"wood-bi/internal/pkg/page"
	"wood-bi/internal/pkg/response"
	"wood-bi/internal/port"

	"github.com/xuri/excelize/v2"
)

type memRepo struct {
	byID   map[int64]*Chart
	nextID int64
	mu     sync.Mutex
}

func newMemRepo() *memRepo {
	return &memRepo{byID: make(map[int64]*Chart), nextID: 1}
}

func (m *memRepo) Create(_ context.Context, c *Chart) (*Chart, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *c
	cp.ID = m.nextID
	m.nextID++
	cp.CreatedAt = time.Now()
	cp.UpdatedAt = cp.CreatedAt
	m.byID[cp.ID] = &cp
	out := cp
	return &out, nil
}

func (m *memRepo) GetByID(_ context.Context, id int64) (*Chart, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (m *memRepo) Update(_ context.Context, id int64, mut UpdateMutation) (*Chart, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	if mut.Name != nil {
		c.Name = *mut.Name
	}
	if mut.Goal != nil {
		c.Goal = *mut.Goal
	}
	if mut.ChartData != nil {
		c.ChartData = *mut.ChartData
	}
	if mut.GenChart != nil {
		c.GenChart = *mut.GenChart
	}
	if mut.GenResult != nil {
		c.GenResult = *mut.GenResult
	}
	if mut.Status != nil {
		c.Status = *mut.Status
	}
	if mut.ExecMessage != nil {
		c.ExecMessage = *mut.ExecMessage
	}
	c.UpdatedAt = time.Now()
	cp := *c
	return &cp, nil
}

func (m *memRepo) SoftDelete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byID, id)
	return nil
}

func (m *memRepo) ListPage(_ context.Context, q QueryParams) ([]*Chart, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []*Chart
	for _, c := range m.byID {
		if q.UserID > 0 && c.UserID != q.UserID {
			continue
		}
		all = append(all, c)
	}
	return all, int64(len(all)), nil
}

func (m *memRepo) ListByStatusOlderThan(_ context.Context, status string, before time.Time, limit int) ([]*Chart, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Chart
	for _, c := range m.byID {
		if c.Status == status && c.UpdatedAt.Before(before) {
			cp := *c
			out = append(out, &cp)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type mockAI struct {
	content string
	err     error
}

func (m *mockAI) Chat(context.Context, port.ChatRequest) (*port.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &port.ChatResponse{Content: m.content}, nil
}

type mockLimiter struct {
	err error
}

func (m *mockLimiter) Allow(context.Context, string, int, time.Duration) error {
	return m.err
}

type mockQueue struct {
	ids []int64
	err error
}

func (m *mockQueue) EnqueueGen(_ context.Context, chartID int64) error {
	if m.err != nil {
		return m.err
	}
	m.ids = append(m.ids, chartID)
	return nil
}

func sampleXLSX(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	_ = f.SetCellValue(sheet, "A1", "日期")
	_ = f.SetCellValue(sheet, "B1", "用户数")
	_ = f.SetCellValue(sheet, "A2", "1号")
	_ = f.SetCellValue(sheet, "B2", 10)
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGenerateSyncOK(t *testing.T) {
	repo := newMemRepo()
	ai := &mockAI{content: `{"option":{"series":[]},"conclusion":"趋势向上"}`}
	svc := NewService(repo, ai, &mockLimiter{}, nil)

	res, err := svc.GenerateSync(context.Background(), 7, GenInput{
		Name:      "增长",
		Goal:      "分析网站用户增长",
		ChartType: "折线图",
		FileBytes: sampleXLSX(t),
		Filename:  "data.xlsx",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ChartID <= 0 || res.GenResult != "趋势向上" {
		t.Fatalf("%+v", res)
	}
	row, _ := repo.GetByID(context.Background(), res.ChartID)
	if row.Status != StatusSucceed || row.UserID != 7 {
		t.Fatalf("%+v", row)
	}
}

func TestGenerateAsyncAndProcessJob(t *testing.T) {
	repo := newMemRepo()
	q := &mockQueue{}
	ai := &mockAI{content: `{"option":{"x":1},"conclusion":"异步结论"}`}
	svc := NewService(repo, ai, nil, q)

	sub, err := svc.GenerateAsync(context.Background(), 3, GenInput{
		Goal: "分析", FileBytes: sampleXLSX(t), Filename: "a.xlsx",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.ids) != 1 || q.ids[0] != sub.ChartID {
		t.Fatalf("queue=%v chartId=%d", q.ids, sub.ChartID)
	}
	row, _ := repo.GetByID(context.Background(), sub.ChartID)
	if row.Status != StatusWait {
		t.Fatalf("status=%s", row.Status)
	}

	if err := svc.ProcessGenJob(context.Background(), sub.ChartID); err != nil {
		t.Fatal(err)
	}
	row, _ = repo.GetByID(context.Background(), sub.ChartID)
	if row.Status != StatusSucceed || row.GenResult != "异步结论" {
		t.Fatalf("%+v", row)
	}

	// 幂等：已成功再跑
	if err := svc.ProcessGenJob(context.Background(), sub.ChartID); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateAsyncEnqueueFail(t *testing.T) {
	svc := NewService(newMemRepo(), &mockAI{content: `{"option":{},"conclusion":"x"}`}, nil, &mockQueue{err: errors.New("mq down")})
	_, err := svc.GenerateAsync(context.Background(), 1, GenInput{
		Goal: "g", FileBytes: sampleXLSX(t), Filename: "a.xlsx",
	})
	if err == nil {
		t.Fatal("expected enqueue error")
	}
}

func TestProcessGenJobAIFailMarksFailed(t *testing.T) {
	repo := newMemRepo()
	row, _ := repo.Create(context.Background(), &Chart{
		Goal: "g", ChartData: "a,b\n1,2", Status: StatusWait, UserID: 1,
	})
	svc := NewService(repo, &mockAI{err: errors.New("boom")}, nil, nil)
	if err := svc.ProcessGenJob(context.Background(), row.ID); err != nil {
		t.Fatal(err) // should ack path: nil
	}
	got, _ := repo.GetByID(context.Background(), row.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestGenerateSyncRateLimit(t *testing.T) {
	svc := NewService(newMemRepo(), &mockAI{content: `{"option":{},"conclusion":"x"}`}, &mockLimiter{err: port.ErrRateLimited}, nil)
	_, err := svc.GenerateSync(context.Background(), 1, GenInput{
		Goal: "g", FileBytes: sampleXLSX(t), Filename: "a.xlsx",
	})
	if err == nil || !response.IsBizError(err) {
		t.Fatalf("expected rate limit biz error, got %v", err)
	}
}

func TestGenerateSyncValidation(t *testing.T) {
	svc := NewService(newMemRepo(), &mockAI{}, nil, nil)
	_, err := svc.GenerateSync(context.Background(), 1, GenInput{Goal: "", Filename: "a.xlsx", FileBytes: []byte{1}})
	if err == nil {
		t.Fatal("expected goal error")
	}
}

func TestGenerateSyncAIError(t *testing.T) {
	svc := NewService(newMemRepo(), &mockAI{err: errors.New("boom")}, nil, nil)
	_, err := svc.GenerateSync(context.Background(), 1, GenInput{
		Goal: "g", FileBytes: sampleXLSX(t), Filename: "a.xlsx",
	})
	if err == nil {
		t.Fatal("expected ai error")
	}
}

func TestCRUD(t *testing.T) {
	svc := NewService(newMemRepo(), nil, nil, nil)
	ctx := context.Background()
	id, err := svc.Create(ctx, 1, CreateInput{Name: "n", Goal: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Edit(ctx, 1, false, EditInput{ID: id, Name: "n2", Goal: "g2"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, 2, false, id); err == nil {
		t.Fatal("expected no auth")
	}
	if err := svc.Delete(ctx, 1, false, id); err != nil {
		t.Fatal(err)
	}
	pageResp, err := svc.ListMyPage(ctx, 1, QueryParams{PageRequest: page.PageRequest{PageNum: 1, PageSize: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if pageResp.Total != 0 {
		t.Fatalf("total=%d", pageResp.Total)
	}
}

func TestCompensateStaleJobs(t *testing.T) {
	repo := newMemRepo()
	q := &mockQueue{}
	svc := NewService(repo, nil, nil, q)

	// 人为制造陈旧 wait
	row, _ := repo.Create(context.Background(), &Chart{Status: StatusWait, UserID: 1, ChartData: "x"})
	repo.mu.Lock()
	repo.byID[row.ID].UpdatedAt = time.Now().Add(-10 * time.Minute)
	repo.mu.Unlock()

	// 陈旧 running
	run, _ := repo.Create(context.Background(), &Chart{Status: StatusRunning, UserID: 1})
	repo.mu.Lock()
	repo.byID[run.ID].UpdatedAt = time.Now().Add(-20 * time.Minute)
	repo.mu.Unlock()

	svc.CompensateStaleJobs(context.Background())

	if len(q.ids) != 1 || q.ids[0] != row.ID {
		t.Fatalf("requeue ids=%v", q.ids)
	}
	got, _ := repo.GetByID(context.Background(), run.ID)
	if got.Status != StatusFailed {
		t.Fatalf("running timeout status=%s", got.Status)
	}
}
