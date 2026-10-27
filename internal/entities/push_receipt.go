package entities

import "time"

// PushReceipt survives inbox and usage retention to prevent delayed replay double counting.
type PushReceipt struct {
	SourceID        string    `gorm:"primaryKey;autoIncrement:false;size:128"`
	DeliveryEventID string    `gorm:"primaryKey;autoIncrement:false;size:128"`
	PayloadHash     string    `gorm:"not null"`
	InboxID         int64     `gorm:"not null"`
	ReceivedAt      time.Time `gorm:"serializer:storageTime;not null;index:idx_push_receipts_received_at"`
}
