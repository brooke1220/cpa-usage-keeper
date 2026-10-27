package entities

import "time"

const (
	// CPAAPIKeyAliasSourceManual 表示别名由管理员在 Keeper 中设置或清空。
	CPAAPIKeyAliasSourceManual = "manual"
	// CPAAPIKeyAliasSourceCPA 表示别名从 CPAMC 浏览器名称导入。
	CPAAPIKeyAliasSourceCPA = "cpa"
)

// CPAAPIKey 保存 CPA 管理接口同步到本地的 API-Key，完整 key 仅供后端内部查询使用。
type CPAAPIKey struct {
	ID                   int64  `gorm:"primaryKey"`
	APIKey               string `gorm:"uniqueIndex:uniq_cpa_api_keys_api_key"`
	DisplayKey           string
	KeyAlias             string
	LocalRankingAvatarID *uint8 `gorm:"column:local_ranking_avatar_id;type:integer"`
	// KeyAliasSource 为空表示从未设置过；manual 的空别名是管理员的明确选择，自动导入不会回填。
	KeyAliasSource string `gorm:"not null;default:''"`
	// PullSeen 表示拉取的 CPA 当前仍列出这个 Key；只有它为真时才允许 Key 自助登录。
	// 拉取同步只改自己的标记，避免把推送带来的其他 CPA 的 Key 误删。
	PullSeen bool `gorm:"not null;default:true"`
	// PushSeen 表示至少收到过一条带这个 Key 的推送用量。
	PushSeen     bool       `gorm:"not null;default:false"`
	IsDeleted    bool       `gorm:"index:idx_cpa_api_keys_is_deleted"`
	LastSyncedAt *time.Time `gorm:"serializer:storageTime"`
	CreatedAt    time.Time  `gorm:"serializer:storageTime"`
	UpdatedAt    time.Time  `gorm:"serializer:storageTime"`
}

// CPAAPIKeyAlias identifies a local key without exposing its credential.
type CPAAPIKeyAlias struct {
	ID       int64
	KeyAlias string
}
