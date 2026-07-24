package user

import (
	"time"

	"wood-bi/internal/pkg/page"
)

// Role 用户角色。
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// User 领域用户（不含密码哈希）。
type User struct {
	ID        int64      `json:"id"`
	Account   string     `json:"account"`
	Name      string     `json:"name,omitempty"`
	Avatar    string     `json:"avatar,omitempty"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `json:"-"`
}

// RegisterInput 用户注册。
type RegisterInput struct {
	Account       string `json:"account" binding:"required"`
	Password      string `json:"password" binding:"required"`
	CheckPassword string `json:"checkPassword" binding:"required"`
}

// LoginInput 用户登录。
type LoginInput struct {
	Account  string `json:"account" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// UpdateMyInput 更新当前用户资料。
type UpdateMyInput struct {
	Name   *string `json:"name"`
	Avatar *string `json:"avatar"`
}

// AdminCreateInput 管理员创建用户。
type AdminCreateInput struct {
	Account  string  `json:"account" binding:"required"`
	Password string  `json:"password" binding:"required"`
	Name     *string `json:"name"`
	Avatar   *string `json:"avatar"`
	Role     string  `json:"role"`
}

// AdminUpdateInput 管理员更新用户。
type AdminUpdateInput struct {
	ID       int64   `json:"id" binding:"required"`
	Name     *string `json:"name"`
	Avatar   *string `json:"avatar"`
	Role     *string `json:"role"`
	Password *string `json:"password"`
}

// QueryParams 用户分页查询。
type QueryParams struct {
	page.PageRequest
	ID      int64  `json:"id" form:"id"`
	Account string `json:"account" form:"account"`
	Name    string `json:"name" form:"name"`
	Role    string `json:"role" form:"role"`
}
