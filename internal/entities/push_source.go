package entities

import "time"

// PushSource 是管理员在设置页登记的一台推送 CPA。SourceID 一经生成永不改变，
// 去重凭据和事件来源都依赖它；Name 只用于展示，可以随时修改。
type PushSource struct {
	ID       int64  `gorm:"primaryKey"`
	SourceID string `gorm:"not null;size:128;uniqueIndex:uniq_push_sources_source_id"`
	Name     string `gorm:"not null;size:64"`
	// TokenSHA256 只保存上传 token 的哈希；明文只在创建或更换时返回一次。
	TokenSHA256 string `gorm:"not null;size:64;uniqueIndex:uniq_push_sources_token_sha256"`
	// TokenHint 保存明文末尾几位，方便管理员核对 CPA 上填的是哪一个 token。
	TokenHint  string     `gorm:"not null;size:8;default:''"`
	RevokedAt  *time.Time `gorm:"serializer:storageTime"`
	LastUsedAt *time.Time `gorm:"serializer:storageTime"`
	CreatedAt  time.Time  `gorm:"serializer:storageTime"`
	UpdatedAt  time.Time  `gorm:"serializer:storageTime"`
}
