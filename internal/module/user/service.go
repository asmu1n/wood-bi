package user

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"wood-bi/internal/pkg/logger"
	"wood-bi/internal/pkg/page"
	"wood-bi/internal/pkg/response"

	"golang.org/x/crypto/bcrypt"
)

const (
	minAccountLen  = 4
	minPasswordLen = 8
	bcryptCost     = bcrypt.DefaultCost
)

// Service 用户用例。
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository) *Service {
	return &Service{
		repo: repo,
		log:  logger.Module("user"),
	}
}

// Register 注册新用户，返回用户 ID。
func (s *Service) Register(ctx context.Context, in RegisterInput) (int64, error) {
	account := strings.TrimSpace(in.Account)
	if err := validateAccountPassword(account, in.Password, in.CheckPassword); err != nil {
		return 0, err
	}

	exists, err := s.repo.ExistsAccount(ctx, account)
	if err != nil {
		return 0, err
	}
	if exists {
		return 0, response.NewBizErrorWithDetail(response.ParamsError, "账号已存在")
	}

	hash, err := hashPassword(in.Password)
	if err != nil {
		return 0, err
	}

	u, err := s.repo.Create(ctx, account, hash, nil, nil, RoleUser)
	if err != nil {
		return 0, err
	}

	s.log.Info("user registered",
		logger.FieldPurpose, logger.PurposeBiz,
		logger.FieldEvent, "user.register",
		"userId", u.ID,
		"account", account,
	)
	return u.ID, nil
}

// Login 校验账号密码，返回用户 VO（调用方负责写 Session）。
func (s *Service) Login(ctx context.Context, in LoginInput) (*User, error) {
	account := strings.TrimSpace(in.Account)
	if account == "" || in.Password == "" {
		return nil, response.NewBizError(response.ParamsError)
	}
	if utf8.RuneCountInString(account) < minAccountLen {
		return nil, response.NewBizErrorWithDetail(response.ParamsError, "账号错误")
	}
	if utf8.RuneCountInString(in.Password) < minPasswordLen {
		return nil, response.NewBizErrorWithDetail(response.ParamsError, "密码错误")
	}

	u, hash, err := s.repo.GetCredentialByAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, response.NewBizErrorWithDetail(response.ParamsError, "用户不存在或密码错误")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)); err != nil {
		return nil, response.NewBizErrorWithDetail(response.ParamsError, "用户不存在或密码错误")
	}

	s.log.Info("user login",
		logger.FieldPurpose, logger.PurposeAudit,
		logger.FieldEvent, "user.login",
		"userId", u.ID,
	)
	return u, nil
}

// GetByID 按 ID 获取未删除用户。
func (s *Service) GetByID(ctx context.Context, id int64) (*User, error) {
	if id <= 0 {
		return nil, response.NewBizError(response.ParamsError)
	}
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, response.NewBizError(response.NotFound)
	}
	return u, nil
}

// UpdateMy 更新当前用户昵称/头像。
func (s *Service) UpdateMy(ctx context.Context, userID int64, in UpdateMyInput) (*User, error) {
	if userID <= 0 {
		return nil, response.NewBizError(response.NotLogin)
	}
	if in.Name == nil && in.Avatar == nil {
		return nil, response.NewBizErrorWithDetail(response.ParamsError, "无更新字段")
	}
	u, err := s.repo.UpdateProfile(ctx, userID, in.Name, in.Avatar)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, response.NewBizError(response.NotFound)
	}
	return u, nil
}

// AdminCreate 管理员创建用户。
func (s *Service) AdminCreate(ctx context.Context, in AdminCreateInput) (int64, error) {
	account := strings.TrimSpace(in.Account)
	if err := validateAccountPassword(account, in.Password, in.Password); err != nil {
		return 0, err
	}
	role := strings.TrimSpace(in.Role)
	if role == "" {
		role = RoleUser
	}
	if role != RoleUser && role != RoleAdmin {
		return 0, response.NewBizErrorWithDetail(response.ParamsError, "角色非法")
	}

	exists, err := s.repo.ExistsAccount(ctx, account)
	if err != nil {
		return 0, err
	}
	if exists {
		return 0, response.NewBizErrorWithDetail(response.ParamsError, "账号已存在")
	}

	hash, err := hashPassword(in.Password)
	if err != nil {
		return 0, err
	}
	u, err := s.repo.Create(ctx, account, hash, in.Name, in.Avatar, role)
	if err != nil {
		return 0, err
	}
	return u.ID, nil
}

// AdminUpdate 管理员更新用户。
func (s *Service) AdminUpdate(ctx context.Context, in AdminUpdateInput) (*User, error) {
	if in.ID <= 0 {
		return nil, response.NewBizError(response.ParamsError)
	}
	if in.Role != nil {
		r := strings.TrimSpace(*in.Role)
		if r != RoleUser && r != RoleAdmin {
			return nil, response.NewBizErrorWithDetail(response.ParamsError, "角色非法")
		}
		in.Role = &r
	}

	var hash *string
	if in.Password != nil && *in.Password != "" {
		if utf8.RuneCountInString(*in.Password) < minPasswordLen {
			return nil, response.NewBizErrorWithDetail(response.ParamsError, "密码过短")
		}
		h, err := hashPassword(*in.Password)
		if err != nil {
			return nil, err
		}
		hash = &h
	}

	u, err := s.repo.UpdateAdmin(ctx, in.ID, in.Name, in.Avatar, in.Role, hash)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, response.NewBizError(response.NotFound)
	}
	return u, nil
}

// AdminDelete 管理员软删除用户。
func (s *Service) AdminDelete(ctx context.Context, id int64) error {
	if id <= 0 {
		return response.NewBizError(response.ParamsError)
	}
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if u == nil {
		return response.NewBizError(response.NotFound)
	}
	return s.repo.SoftDelete(ctx, id)
}

// ListPage 分页查询用户（管理端）。
func (s *Service) ListPage(ctx context.Context, q QueryParams) (*page.PageResponse[*User], error) {
	rows, total, err := s.repo.ListPage(ctx, q)
	if err != nil {
		return nil, err
	}
	return page.NewPageResponse(rows, total, q.PageRequest), nil
}

// IsAdmin 判断角色是否为管理员。
func IsAdmin(role string) bool {
	return role == RoleAdmin
}

func validateAccountPassword(account, password, checkPassword string) error {
	if account == "" || password == "" || checkPassword == "" {
		return response.NewBizErrorWithDetail(response.ParamsError, "参数为空")
	}
	if utf8.RuneCountInString(account) < minAccountLen {
		return response.NewBizErrorWithDetail(response.ParamsError, "用户账号过短")
	}
	if utf8.RuneCountInString(password) < minPasswordLen {
		return response.NewBizErrorWithDetail(response.ParamsError, "用户密码过短")
	}
	if password != checkPassword {
		return response.NewBizErrorWithDetail(response.ParamsError, "两次输入的密码不一致")
	}
	return nil
}

func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
