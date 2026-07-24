package middleware

import (
	"net/http"

	"wood-bi/internal/pkg/response"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// Session 键与上下文键。
const (
	SessionKeyUserID   = "userID"
	SessionKeyUserRole = "userRole"
)

// AuthRequired 要求请求已登录，否则返回 401。
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		uid := session.Get(SessionKeyUserID)
		if uid == nil {
			c.JSON(http.StatusUnauthorized, response.FailWithCode(response.NotLogin, ""))
			c.Abort()
			return
		}
		c.Set(SessionKeyUserID, uid)
		if role := session.Get(SessionKeyUserRole); role != nil {
			c.Set(SessionKeyUserRole, role)
		}
		c.Next()
	}
}

// AdminRequired 要求当前会话角色为 admin（须在 AuthRequired 之后使用）。
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, err := GetLoginUserRole(c)
		if err != nil || role != "admin" {
			c.JSON(http.StatusForbidden, response.FailWithCode(response.NoAuth, ""))
			c.Abort()
			return
		}
		c.Next()
	}
}

// GetLoginUserID 从请求上下文读取当前登录用户 ID。
func GetLoginUserID(c *gin.Context) (int64, error) {
	uid, exists := c.Get(SessionKeyUserID)
	if !exists {
		return 0, response.NewBizError(response.NotLogin)
	}
	switch v := uid.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		// JSON 反序列化 session 时可能落到 float64
		return int64(v), nil
	default:
		return 0, response.NewBizError(response.NotLogin)
	}
}

// GetLoginUserRole 从请求上下文读取当前登录用户角色。
func GetLoginUserRole(c *gin.Context) (string, error) {
	role, exists := c.Get(SessionKeyUserRole)
	if !exists {
		// 兼容仅写了 userID 的旧 session：视为非管理员
		return "", response.NewBizError(response.NoAuth)
	}
	s, ok := role.(string)
	if !ok || s == "" {
		return "", response.NewBizError(response.NoAuth)
	}
	return s, nil
}
