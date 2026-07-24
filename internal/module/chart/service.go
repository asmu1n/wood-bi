package chart

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"wood-bi/internal/pkg/logger"
	"wood-bi/internal/pkg/page"
	"wood-bi/internal/pkg/response"
	"wood-bi/internal/port"
)

const (
	maxFileSize     = 1 << 20 // 1MB
	maxNameLen      = 100
	maxPageSizeHard = 20 // 列表防爬，比 page 默认 max 更严
	rateLimitKeyFmt = "genChartByAi_%d"
	rateLimitCount  = 2
	rateLimitWindow = time.Second
)

// Service 图表用例。
type Service struct {
	repo    Repository
	ai      port.AI
	limiter port.RateLimiter
	log     *slog.Logger
}

// NewService 创建图表服务。ai / limiter 可为 nil（仅 CRUD 时）；GenerateSync 需要二者。
func NewService(repo Repository, ai port.AI, limiter port.RateLimiter) *Service {
	return &Service{
		repo:    repo,
		ai:      ai,
		limiter: limiter,
		log:     logger.Module("chart"),
	}
}

// Create 创建图表记录。
func (s *Service) Create(ctx context.Context, userID int64, in CreateInput) (int64, error) {
	if userID <= 0 {
		return 0, response.NewBizError(response.NotLogin)
	}
	c := &Chart{
		Name:      strings.TrimSpace(in.Name),
		Goal:      strings.TrimSpace(in.Goal),
		ChartData: in.ChartData,
		ChartType: strings.TrimSpace(in.ChartType),
		Status:    StatusWait,
		UserID:    userID,
	}
	row, err := s.repo.Create(ctx, c)
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

// Delete 删除（本人或管理员）。
func (s *Service) Delete(ctx context.Context, operatorID int64, isAdmin bool, id int64) error {
	if id <= 0 {
		return response.NewBizError(response.ParamsError)
	}
	old, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if old == nil {
		return response.NewBizError(response.NotFound)
	}
	if old.UserID != operatorID && !isAdmin {
		return response.NewBizError(response.NoAuth)
	}
	return s.repo.SoftDelete(ctx, id)
}

// GetByID 详情。
func (s *Service) GetByID(ctx context.Context, id int64) (*Chart, error) {
	if id <= 0 {
		return nil, response.NewBizError(response.ParamsError)
	}
	c, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, response.NewBizError(response.NotFound)
	}
	return c, nil
}

// Edit 用户编辑基础字段。
func (s *Service) Edit(ctx context.Context, operatorID int64, isAdmin bool, in EditInput) error {
	if in.ID <= 0 {
		return response.NewBizError(response.ParamsError)
	}
	old, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return err
	}
	if old == nil {
		return response.NewBizError(response.NotFound)
	}
	if old.UserID != operatorID && !isAdmin {
		return response.NewBizError(response.NoAuth)
	}
	name, goal, data, typ := in.Name, in.Goal, in.ChartData, in.ChartType
	_, err = s.repo.Update(ctx, in.ID, UpdateMutation{
		Name:      &name,
		Goal:      &goal,
		ChartData: &data,
		ChartType: &typ,
	})
	return err
}

// AdminUpdate 管理员更新。
func (s *Service) AdminUpdate(ctx context.Context, in AdminUpdateInput) error {
	if in.ID <= 0 {
		return response.NewBizError(response.ParamsError)
	}
	old, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return err
	}
	if old == nil {
		return response.NewBizError(response.NotFound)
	}
	_, err = s.repo.Update(ctx, in.ID, UpdateMutation{
		Name:        in.Name,
		Goal:        in.Goal,
		ChartData:   in.ChartData,
		ChartType:   in.ChartType,
		GenChart:    in.GenChart,
		GenResult:   in.GenResult,
		Status:      in.Status,
		ExecMessage: in.ExecMessage,
	})
	return err
}

// ListPage 分页列表（可带 userId 等过滤）。
func (s *Service) ListPage(ctx context.Context, q QueryParams) (*page.PageResponse[*Chart], error) {
	if q.PageSize > maxPageSizeHard {
		return nil, response.NewBizErrorWithDetail(response.ParamsError, "pageSize 过大")
	}
	rows, total, err := s.repo.ListPage(ctx, q)
	if err != nil {
		return nil, err
	}
	return page.NewPageResponse(rows, total, q.PageRequest), nil
}

// ListMyPage 当前用户的图表分页。
func (s *Service) ListMyPage(ctx context.Context, userID int64, q QueryParams) (*page.PageResponse[*Chart], error) {
	if userID <= 0 {
		return nil, response.NewBizError(response.NotLogin)
	}
	q.UserID = userID
	return s.ListPage(ctx, q)
}

// GenerateSync 同步 AI 生成图表。
func (s *Service) GenerateSync(ctx context.Context, userID int64, in GenInput) (*GenResult, error) {
	if userID <= 0 {
		return nil, response.NewBizError(response.NotLogin)
	}
	if s.ai == nil {
		return nil, response.NewBizErrorWithDetail(response.SystemError, "AI 服务未配置")
	}
	if err := validateGenInput(in); err != nil {
		return nil, err
	}

	if s.limiter != nil {
		key := fmt.Sprintf(rateLimitKeyFmt, userID)
		if err := s.limiter.Allow(ctx, key, rateLimitCount, rateLimitWindow); err != nil {
			if errors.Is(err, port.ErrRateLimited) {
				return nil, response.NewBizError(response.TooManyRequests)
			}
			return nil, err
		}
	}

	csvData, err := ExcelToCSV(in.FileBytes)
	if err != nil {
		return nil, response.NewBizErrorWithDetail(response.ParamsError, "表格解析失败: "+err.Error())
	}
	if strings.TrimSpace(csvData) == "" {
		return nil, response.NewBizErrorWithDetail(response.ParamsError, "表格数据为空")
	}

	userPrompt := BuildUserPrompt(in.Goal, in.ChartType, csvData)
	aiResp, err := s.ai.Chat(ctx, port.ChatRequest{
		JSONMode: true,
		Messages: []port.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		s.log.Error("ai chat failed",
			logger.FieldPurpose, logger.PurposeBiz,
			logger.FieldEvent, "chart.gen_ai_error",
			logger.FieldErr, err,
			"userId", userID,
		)
		return nil, response.NewBizErrorWithDetail(response.SystemError, "AI 调用失败")
	}

	parsed, err := ParseAIOutput(aiResp.Content)
	if err != nil {
		s.log.Error("ai parse failed",
			logger.FieldPurpose, logger.PurposeBiz,
			logger.FieldEvent, "chart.gen_parse_error",
			logger.FieldErr, err,
			"userId", userID,
		)
		return nil, response.NewBizErrorWithDetail(response.SystemError, "AI 生成结果解析失败")
	}

	optionStr, err := OptionJSON(parsed.Option)
	if err != nil {
		return nil, err
	}

	row, err := s.repo.Create(ctx, &Chart{
		Name:      strings.TrimSpace(in.Name),
		Goal:      strings.TrimSpace(in.Goal),
		ChartData: csvData,
		ChartType: strings.TrimSpace(in.ChartType),
		GenChart:  optionStr,
		GenResult: parsed.Conclusion,
		Status:    StatusSucceed,
		UserID:    userID,
	})
	if err != nil {
		return nil, err
	}

	s.log.Info("chart generated",
		logger.FieldPurpose, logger.PurposeBiz,
		logger.FieldEvent, "chart.gen_sync_ok",
		"chartId", row.ID,
		"userId", userID,
	)

	return &GenResult{
		ChartID:   row.ID,
		GenChart:  optionStr,
		GenResult: parsed.Conclusion,
	}, nil
}

func validateGenInput(in GenInput) error {
	if strings.TrimSpace(in.Goal) == "" {
		return response.NewBizErrorWithDetail(response.ParamsError, "目标为空")
	}
	if name := strings.TrimSpace(in.Name); name != "" && utf8.RuneCountInString(name) > maxNameLen {
		return response.NewBizErrorWithDetail(response.ParamsError, "名称过长")
	}
	if len(in.FileBytes) == 0 {
		return response.NewBizErrorWithDetail(response.ParamsError, "文件为空")
	}
	if len(in.FileBytes) > maxFileSize {
		return response.NewBizErrorWithDetail(response.ParamsError, "文件超过 1M")
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(in.Filename), "."))
	if ext != "xlsx" && ext != "xls" {
		return response.NewBizErrorWithDetail(response.ParamsError, "文件后缀非法")
	}
	return nil
}
