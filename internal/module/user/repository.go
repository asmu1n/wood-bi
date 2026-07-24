package user

import "context"

// Repository 用户持久化端口（由 repo 包实现）。
type Repository interface {
	Create(ctx context.Context, account, passwordHash string, name, avatar *string, role string) (*User, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	// GetByAccount 按账号查询未删除用户；找不到返回 (nil, nil)。
	GetByAccount(ctx context.Context, account string) (*User, error)
	// GetCredentialByAccount 返回用户与密码哈希；找不到返回 (nil, "", nil)。
	GetCredentialByAccount(ctx context.Context, account string) (*User, string, error)
	UpdateProfile(ctx context.Context, id int64, name, avatar *string) (*User, error)
	UpdateAdmin(ctx context.Context, id int64, name, avatar, role *string, passwordHash *string) (*User, error)
	SoftDelete(ctx context.Context, id int64) error
	ListPage(ctx context.Context, q QueryParams) ([]*User, int64, error)
	ExistsAccount(ctx context.Context, account string) (bool, error)
}
