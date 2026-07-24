package chart

import (
	"context"
	"errors"
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
}

func newMemRepo() *memRepo {
	return &memRepo{byID: make(map[int64]*Chart), nextID: 1}
}

func (m *memRepo) Create(_ context.Context, c *Chart) (*Chart, error) {
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
	c, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (m *memRepo) Update(_ context.Context, id int64, mut UpdateMutation) (*Chart, error) {
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
	if mut.Status != nil {
		c.Status = *mut.Status
	}
	cp := *c
	return &cp, nil
}

func (m *memRepo) SoftDelete(_ context.Context, id int64) error {
	delete(m.byID, id)
	return nil
}

func (m *memRepo) ListPage(_ context.Context, q QueryParams) ([]*Chart, int64, error) {
	var all []*Chart
	for _, c := range m.byID {
		if q.UserID > 0 && c.UserID != q.UserID {
			continue
		}
		all = append(all, c)
	}
	return all, int64(len(all)), nil
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
	svc := NewService(repo, ai, &mockLimiter{})

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

func TestGenerateSyncRateLimit(t *testing.T) {
	svc := NewService(newMemRepo(), &mockAI{content: `{"option":{},"conclusion":"x"}`}, &mockLimiter{err: port.ErrRateLimited})
	_, err := svc.GenerateSync(context.Background(), 1, GenInput{
		Goal: "g", FileBytes: sampleXLSX(t), Filename: "a.xlsx",
	})
	if err == nil || !response.IsBizError(err) {
		t.Fatalf("expected rate limit biz error, got %v", err)
	}
}

func TestGenerateSyncValidation(t *testing.T) {
	svc := NewService(newMemRepo(), &mockAI{}, nil)
	_, err := svc.GenerateSync(context.Background(), 1, GenInput{Goal: "", Filename: "a.xlsx", FileBytes: []byte{1}})
	if err == nil {
		t.Fatal("expected goal error")
	}
}

func TestGenerateSyncAIError(t *testing.T) {
	svc := NewService(newMemRepo(), &mockAI{err: errors.New("boom")}, nil)
	_, err := svc.GenerateSync(context.Background(), 1, GenInput{
		Goal: "g", FileBytes: sampleXLSX(t), Filename: "a.xlsx",
	})
	if err == nil {
		t.Fatal("expected ai error")
	}
}

func TestCRUD(t *testing.T) {
	svc := NewService(newMemRepo(), nil, nil)
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
