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

	// 补偿任务阈值
	staleWaitAfter    = 5 * time.Minute
	staleRunningAfter = 10 * time.Minute
	compensateLimit   = 50
)

// Service 图表用例。
type Service struct {
	repo    Repository
	ai      port.AI
	limiter port.RateLimiter
	queue   port.ChartGenQueue
	log     *slog.Logger
}

// NewService 创建图表服务；queue 为异步生成任务投递端口（如 Rabbit Publisher）。
// 生产路径由 cmd 保证 ai/limiter/queue 非 nil；单测可按需传 nil。
func NewService(repo Repository, ai port.AI, limiter port.RateLimiter, queue port.ChartGenQueue) *Service {
	return &Service{
		repo:    repo,
		ai:      ai,
		limiter: limiter,
		queue:   queue,
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
	// 防御：生产启动已校验 AI；此处保留便于单测。
	if s.ai == nil {
		return nil, response.NewBizErrorWithDetail(response.SystemError, "AI 服务未配置")
	}
	if err := validateGenInput(in); err != nil {
		return nil, err
	}
	if err := s.rateLimit(ctx, userID); err != nil {
		return nil, err
	}

	csvData, err := excelToCSV(in.FileBytes)
	if err != nil {
		return nil, err
	}

	optionStr, conclusion, err := s.callAI(ctx, in.Goal, in.ChartType, csvData)
	if err != nil {
		return nil, err
	}

	row, err := s.repo.Create(ctx, &Chart{
		Name:      strings.TrimSpace(in.Name),
		Goal:      strings.TrimSpace(in.Goal),
		ChartData: csvData,
		ChartType: strings.TrimSpace(in.ChartType),
		GenChart:  optionStr,
		GenResult: conclusion,
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
		GenResult: conclusion,
	}, nil
}

// GenerateAsync 异步 AI 生成：落库 wait → 投递 MQ → 立即返回 chartId（前端轮询详情）。
func (s *Service) GenerateAsync(ctx context.Context, userID int64, in GenInput) (*AsyncSubmitResult, error) {
	if userID <= 0 {
		return nil, response.NewBizError(response.NotLogin)
	}
	// 防御：生产启动已校验 queue/AI；此处保留便于单测。
	if s.queue == nil {
		return nil, response.NewBizErrorWithDetail(response.SystemError, "消息队列未配置")
	}
	if s.ai == nil {
		return nil, response.NewBizErrorWithDetail(response.SystemError, "AI 服务未配置")
	}
	if err := validateGenInput(in); err != nil {
		return nil, err
	}
	if err := s.rateLimit(ctx, userID); err != nil {
		return nil, err
	}

	csvData, err := excelToCSV(in.FileBytes)
	if err != nil {
		return nil, err
	}

	row, err := s.repo.Create(ctx, &Chart{
		Name:      strings.TrimSpace(in.Name),
		Goal:      strings.TrimSpace(in.Goal),
		ChartData: csvData,
		ChartType: strings.TrimSpace(in.ChartType),
		Status:    StatusWait,
		UserID:    userID,
	})
	if err != nil {
		return nil, err
	}

	if err := s.queue.EnqueueGen(ctx, row.ID); err != nil {
		msg := "投递消息队列失败: " + err.Error()
		status := StatusFailed
		_, _ = s.repo.Update(ctx, row.ID, UpdateMutation{
			Status:      &status,
			ExecMessage: &msg,
		})
		s.log.Error("enqueue failed",
			logger.FieldPurpose, logger.PurposeBiz,
			logger.FieldEvent, "chart.gen_async_enqueue_error",
			logger.FieldErr, err,
			"chartId", row.ID,
		)
		return nil, response.NewBizErrorWithDetail(response.SystemError, "任务投递失败")
	}

	s.log.Info("chart async submitted",
		logger.FieldPurpose, logger.PurposeBiz,
		logger.FieldEvent, "chart.gen_async_ok",
		"chartId", row.ID,
		"userId", userID,
	)

	return &AsyncSubmitResult{ChartID: row.ID}, nil
}

// ProcessGenJob MQ 消费入口：按 chartID 执行生成状态机（wait/running → AI → succeed|failed）。
// 返回 nil → 消息 ack（含业务失败已写入 failed）；返回 error → 可 requeue（如 DB 瞬时故障）。
func (s *Service) ProcessGenJob(ctx context.Context, chartID int64) error {
	if chartID <= 0 {
		return nil
	}
	c, err := s.repo.GetByID(ctx, chartID)
	if err != nil {
		return err // 瞬时 DB 错误 → requeue
	}
	if c == nil {
		s.log.Warn("chart not found for job",
			logger.FieldPurpose, logger.PurposeBiz,
			logger.FieldEvent, "chart.job_not_found",
			"chartId", chartID,
		)
		return nil // 永久：ack 丢弃
	}
	switch c.Status {
	case StatusSucceed:
		return nil
	case StatusFailed:
		// 已失败不自动重跑（除非补偿把状态改回 wait 再投递）
		return nil
	}

	if s.ai == nil {
		return s.failJob(ctx, chartID, "AI 服务未配置")
	}
	if strings.TrimSpace(c.ChartData) == "" {
		return s.failJob(ctx, chartID, "图表数据为空")
	}

	running := StatusRunning
	if _, err := s.repo.Update(ctx, chartID, UpdateMutation{Status: &running}); err != nil {
		return err
	}

	optionStr, conclusion, err := s.callAI(ctx, c.Goal, c.ChartType, c.ChartData)
	if err != nil {
		// 业务侧 AI/解析失败：落 failed 并 ack，避免毒消息循环
		_ = s.failJob(ctx, chartID, err.Error())
		return nil
	}

	succeed := StatusSucceed
	if _, err := s.repo.Update(ctx, chartID, UpdateMutation{
		GenChart:  &optionStr,
		GenResult: &conclusion,
		Status:    &succeed,
	}); err != nil {
		return err
	}

	s.log.Info("chart job succeed",
		logger.FieldPurpose, logger.PurposeBiz,
		logger.FieldEvent, "chart.job_succeed",
		"chartId", chartID,
	)
	return nil
}

// CompensateStaleJobs 定时补偿卡住的异步任务：滞留 wait 补投 MQ，超时 running 标 failed。
func (s *Service) CompensateStaleJobs(ctx context.Context) {
	now := time.Now()

	// running 超时
	runningRows, err := s.repo.ListByStatusOlderThan(ctx, StatusRunning, now.Add(-staleRunningAfter), compensateLimit)
	if err != nil {
		s.log.Error("list stale running failed",
			logger.FieldPurpose, logger.PurposeJob,
			logger.FieldEvent, "chart.compensate_list_error",
			logger.FieldErr, err,
		)
	} else {
		for _, row := range runningRows {
			_ = s.failJob(ctx, row.ID, "任务执行超时")
			s.log.Warn("marked stale running as failed",
				logger.FieldPurpose, logger.PurposeJob,
				logger.FieldEvent, "chart.compensate_running_timeout",
				"chartId", row.ID,
			)
		}
	}

	// wait 补投
	if s.queue == nil {
		return
	}
	waitRows, err := s.repo.ListByStatusOlderThan(ctx, StatusWait, now.Add(-staleWaitAfter), compensateLimit)
	if err != nil {
		s.log.Error("list stale wait failed",
			logger.FieldPurpose, logger.PurposeJob,
			logger.FieldEvent, "chart.compensate_list_error",
			logger.FieldErr, err,
		)
		return
	}
	for _, row := range waitRows {
		if err := s.queue.EnqueueGen(ctx, row.ID); err != nil {
			s.log.Error("re-enqueue wait failed",
				logger.FieldPurpose, logger.PurposeJob,
				logger.FieldEvent, "chart.compensate_enqueue_error",
				logger.FieldErr, err,
				"chartId", row.ID,
			)
			continue
		}
		// 触碰 updated_at，避免同一任务被反复补投过频
		wait := StatusWait
		_, _ = s.repo.Update(ctx, row.ID, UpdateMutation{Status: &wait})
		s.log.Info("re-enqueued stale wait",
			logger.FieldPurpose, logger.PurposeJob,
			logger.FieldEvent, "chart.compensate_wait_requeue",
			"chartId", row.ID,
		)
	}
}

// failJob 将任务标为 failed 并写入 exec_message（供消费失败与补偿超时使用）。
func (s *Service) failJob(ctx context.Context, chartID int64, msg string) error {
	failed := StatusFailed
	_, err := s.repo.Update(ctx, chartID, UpdateMutation{
		Status:      &failed,
		ExecMessage: &msg,
	})
	if err != nil {
		s.log.Error("update failed status error",
			logger.FieldPurpose, logger.PurposeBiz,
			logger.FieldEvent, "chart.job_fail_update_error",
			logger.FieldErr, err,
			"chartId", chartID,
		)
	}
	return err
}

func (s *Service) rateLimit(ctx context.Context, userID int64) error {
	if s.limiter == nil {
		return nil
	}
	key := fmt.Sprintf(rateLimitKeyFmt, userID)
	if err := s.limiter.Allow(ctx, key, rateLimitCount, rateLimitWindow); err != nil {
		if errors.Is(err, port.ErrRateLimited) {
			return response.NewBizError(response.TooManyRequests)
		}
		return err
	}
	return nil
}

func (s *Service) callAI(ctx context.Context, goal, chartType, csvData string) (optionJSON, conclusion string, err error) {
	userPrompt := BuildUserPrompt(goal, chartType, csvData)
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
		)
		return "", "", response.NewBizErrorWithDetail(response.SystemError, "AI 调用失败")
	}

	parsed, err := ParseAIOutput(aiResp.Content)
	if err != nil {
		s.log.Error("ai parse failed",
			logger.FieldPurpose, logger.PurposeBiz,
			logger.FieldEvent, "chart.gen_parse_error",
			logger.FieldErr, err,
		)
		return "", "", response.NewBizErrorWithDetail(response.SystemError, "AI 生成结果解析失败")
	}

	optionStr, err := OptionJSON(parsed.Option)
	if err != nil {
		return "", "", err
	}
	return optionStr, parsed.Conclusion, nil
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
