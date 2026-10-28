package entities

import "time"

// PushUsageIdentity is display metadata, separate from locally managed credentials.
// AuthIndex includes the immutable CPA source ID, so equal remote indexes cannot collide.
type PushUsageIdentity struct {
	AuthIndex    string `gorm:"primaryKey;size:512"`
	SourceID     string `gorm:"not null;size:128;index:idx_push_usage_identities_source_id"`
	DisplayName  string `gorm:"not null;size:256"`
	ProviderType string `gorm:"not null;size:64;default:''"`
	// 接收端在 SQL 里按 TEXT 比较新旧名称，必须用固定宽度 UTC 的 sortableTime。
	ObservedAt time.Time `gorm:"serializer:sortableTime"`
}
