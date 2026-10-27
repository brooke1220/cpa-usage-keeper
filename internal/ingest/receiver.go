// Package ingest receives authenticated, idempotent usage batches from CPA plugins.
package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"
	"cpa-usage-keeper/internal/timeutil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxBatchBytes = 1 << 20
const MaxBatchEvents = 100

var ErrConflict = errors.New("delivery ID already has different content")
var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type Event struct {
	ID    string          `json:"delivery_event_id"`
	Usage json.RawMessage `json:"usage"`
}
type Batch struct {
	SchemaVersion int     `json:"schema_version"`
	SourceID      string  `json:"source_id"`
	Events        []Event `json:"events"`
}
type Ack struct {
	SchemaVersion int      `json:"schema_version"`
	AcceptedIDs   []string `json:"accepted_event_ids"`
}

type Receiver struct {
	db      *gorm.DB
	sources *Sources
}

// New 创建接收器；来源与开关来自设置页维护的 Sources，修改后立即生效，无需重启。
func New(db *gorm.DB, sources *Sources) (*Receiver, error) {
	if db == nil {
		return nil, fmt.Errorf("push database is required")
	}
	if sources == nil {
		return nil, fmt.Errorf("push sources are required")
	}
	return &Receiver{db: db, sources: sources}, nil
}

// Sources 暴露来源管理，供设置页接口使用。
func (r *Receiver) Sources() *Sources { return r.sources }

func (r *Receiver) authenticate(header string) (int64, string, bool) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || len(token) < 32 || len(token) > 4096 {
		return 0, "", false
	}
	return r.sources.authenticate(token)
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// 开关关闭时按暂时不可用回复：插件保留已落盘数据并退避重试，重新打开后自动补传。
	if !r.sources.Enabled() {
		writeError(w, 503, "push_ingest_disabled")
		return
	}
	sourceKey, sourceID, ok := r.authenticate(req.Header.Get("Authorization"))
	if !ok {
		writeError(w, 401, "invalid_push_credentials")
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, MaxBatchBytes)
	var b Batch
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&b); err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			writeError(w, 413, "batch_too_large")
		} else {
			writeError(w, 400, "invalid_batch")
		}
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid_batch")
		return
	}
	// 来源由 token 决定；新插件不再发送 source_id。旧插件仍会发送，必须与 token 绑定的来源一致。
	if b.SourceID != "" && b.SourceID != sourceID {
		writeError(w, 403, "source_mismatch")
		return
	}
	if b.SchemaVersion != 1 || len(b.Events) == 0 || len(b.Events) > MaxBatchEvents {
		writeError(w, 400, "unsupported_version_or_batch_size")
		return
	}
	for _, e := range b.Events {
		if !identifier.MatchString(e.ID) || validateUsage(e.Usage) != nil {
			writeError(w, 422, "invalid_usage_event")
			return
		}
	}
	now := time.Now()
	ack, err := r.accept(req.Context(), sourceKey, sourceID, b.Events, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			writeError(w, 409, "delivery_id_conflict")
		} else {
			writeError(w, 503, "durable_ingest_unavailable")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(ack)
}

// Accept atomically records receipt, raw inbox data and the non-destructive Key catalog.
// An ACK covers the durable transaction, not asynchronous aggregation completion.
func (r *Receiver) Accept(ctx context.Context, sourceID string, events []Event, now time.Time) (Ack, error) {
	return r.accept(ctx, 0, sourceID, events, now)
}

// accept 在同一事务里写入凭据、原始收件和 Key 目录；sourceKey 为 0 时不更新来源的最近上传时间。
func (r *Receiver) accept(ctx context.Context, sourceKey int64, sourceID string, events []Event, now time.Time) (Ack, error) {
	ack := Ack{SchemaVersion: 1, AcceptedIDs: make([]string, 0, len(events))}
	recordUse := sourceKey > 0 && r.sources.shouldRecordUse(sourceKey, now)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, e := range events {
			hash := sha256.Sum256(e.Usage)
			receipt := entities.PushReceipt{SourceID: sourceID, DeliveryEventID: e.ID, PayloadHash: hex.EncodeToString(hash[:]), ReceivedAt: timeutil.NormalizeStorageTime(now)}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				var existing entities.PushReceipt
				if err := tx.Where("source_id=? AND delivery_event_id=?", sourceID, e.ID).First(&existing).Error; err != nil {
					return err
				}
				if existing.PayloadHash != receipt.PayloadHash {
					return ErrConflict
				}
				ack.AcceptedIDs = append(ack.AcceptedIDs, e.ID)
				continue
			}
			row := entities.RedisUsageInbox{Source: "https_push:" + sourceID, SourceID: sourceID, DeliveryEventID: e.ID, MessageHash: receipt.PayloadHash, RawMessage: string(e.Usage), Status: "pending", PoppedAt: timeutil.NormalizeStorageTime(now)}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			if err := tx.Model(&entities.PushReceipt{}).Where("source_id=? AND delivery_event_id=?", sourceID, e.ID).Update("inbox_id", row.ID).Error; err != nil {
				return err
			}
			var payload usagePayload
			if err := json.Unmarshal(e.Usage, &payload); err != nil {
				return err
			}
			if err := repository.RecordPushedCPAAPIKey(tx, payload.APIKey, now); err != nil {
				return err
			}
			ack.AcceptedIDs = append(ack.AcceptedIDs, e.ID)
		}
		// 最近上传时间随同一事务提交：不在 ACK 之后另写，进程重启或请求取消都不会丢失。
		if recordUse {
			return tx.Model(&entities.PushSource{}).Where("id = ?", sourceKey).Update("last_used_at", timeutil.NormalizeStorageTime(now)).Error
		}
		return nil
	})
	if err != nil && recordUse {
		r.sources.forgetRecordedUse(sourceKey, now)
	}
	return ack, err
}

func validateUsage(raw []byte) error {
	var p usagePayload
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return fmt.Errorf("invalid usage payload")
	}
	if p.Timestamp.IsZero() || len(p.APIKey) > 1024 || strings.TrimSpace(p.ExecutionID) == "" || len(p.ExecutionID) > 128 || strings.TrimSpace(p.Model) == "" || len(p.Model) > 512 || strings.TrimSpace(p.ExecutorType) == "" || p.AccountingVersion != 2 || len(p.TokenBreakdown) == 0 {
		return fmt.Errorf("missing usage fields")
	}
	if p.LatencyMS < 0 || (p.TTFTMS != nil && *p.TTFTMS < 0) {
		return fmt.Errorf("invalid latency")
	}
	for _, n := range []int64{p.Tokens.Input, p.Tokens.Output, p.Tokens.Reasoning, p.Tokens.Cached, p.Tokens.CacheRead, p.Tokens.CacheCreation, p.Tokens.Total} {
		if n < 0 {
			return fmt.Errorf("negative token count")
		}
	}
	var breakdown struct {
		Version      int    `json:"schema_version"`
		Quality      string `json:"quality"`
		Total        int64  `json:"total_tokens"`
		Unclassified int64  `json:"unclassified_tokens"`
		Input        struct {
			Total    int64 `json:"total_tokens"`
			Uncached int64 `json:"uncached_tokens"`
			Read     int64 `json:"cache_read_tokens"`
			Write    int64 `json:"cache_write_tokens"`
		} `json:"input"`
		Output struct {
			Total     int64 `json:"total_tokens"`
			Normal    int64 `json:"non_reasoning_tokens"`
			Reasoning int64 `json:"reasoning_tokens"`
		} `json:"output"`
	}
	dec := json.NewDecoder(bytes.NewReader(p.TokenBreakdown))
	dec.DisallowUnknownFields()
	if dec.Decode(&breakdown) != nil || breakdown.Version != 2 {
		return fmt.Errorf("invalid token accounting")
	}
	if breakdown.Quality != "complete" && breakdown.Quality != "inconsistent" && breakdown.Quality != "unclassified" {
		return fmt.Errorf("invalid accounting quality")
	}
	sum := func(values ...int64) (int64, bool) {
		var total int64
		for _, v := range values {
			if v < 0 || v > math.MaxInt64-total {
				return 0, false
			}
			total += v
		}
		return total, true
	}
	input, okI := sum(breakdown.Input.Uncached, breakdown.Input.Read, breakdown.Input.Write)
	output, okO := sum(breakdown.Output.Normal, breakdown.Output.Reasoning)
	total, okT := sum(breakdown.Input.Total, breakdown.Output.Total, breakdown.Unclassified)
	if !okI || !okO || !okT || input != breakdown.Input.Total || output != breakdown.Output.Total || total != breakdown.Total || (breakdown.Quality == "complete" && breakdown.Unclassified != 0) {
		return fmt.Errorf("invalid accounting buckets")
	}
	_, _, err := service.DecodeRedisUsageMessage(string(raw), p.Timestamp)
	return err
}

type Status struct {
	Enabled        bool           `json:"enabled"`
	LastReceivedAt *time.Time     `json:"last_received_at,omitempty"`
	Pending        int64          `json:"pending"`
	Quarantined    int64          `json:"quarantined"`
	Sources        []SourceStatus `json:"sources"`
}

// SourceStatus 让管理员逐台确认 CPA 是否仍在上报；只有全局时间时，某台 CPA 停报很难被发现。
type SourceStatus struct {
	Name       string     `json:"name"`
	Revoked    bool       `json:"revoked"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Status is polled by every open dashboard. Receipts are retained forever, so it never counts
// them and the inbox counts only cover unfinished rows. Receipts are never deleted and all
// ingest writes are serialized, so rowid order is receipt order; received_at text carries a
// local UTC offset and stops sorting chronologically when that offset changes.
func (r *Receiver) Status(ctx context.Context) (Status, error) {
	s := Status{Enabled: r.sources.Enabled()}
	var latest entities.PushReceipt
	if err := r.db.WithContext(ctx).Select("source_id,received_at").Order("rowid DESC").Limit(1).Find(&latest).Error; err != nil {
		return s, err
	}
	if latest.SourceID != "" {
		v := latest.ReceivedAt
		s.LastReceivedAt = &v
	}
	sources, err := r.sources.List(ctx)
	if err != nil {
		return s, err
	}
	s.Sources = make([]SourceStatus, 0, len(sources))
	for _, source := range sources {
		s.Sources = append(s.Sources, SourceStatus{Name: source.Name, Revoked: source.Revoked, LastUsedAt: source.LastUsedAt})
	}
	if err := r.db.WithContext(ctx).Model(&entities.RedisUsageInbox{}).Where("source_id<>'' AND status IN ?", []string{"pending", "process_failed"}).Count(&s.Pending).Error; err != nil {
		return s, err
	}
	err = r.db.WithContext(ctx).Model(&entities.RedisUsageInbox{}).Where("source_id<>'' AND status IN ?", []string{"quarantined", "decode_failed", "discarded"}).Count(&s.Quarantined).Error
	return s, err
}

type QuarantineItem struct {
	ID              int64  `json:"id"`
	SourceID        string `json:"source_id"`
	SourceName      string `json:"source_name,omitempty"`
	DeliveryEventID string `json:"delivery_event_id"`
	Attempts        int    `json:"attempts"`
}

func (r *Receiver) Quarantine(ctx context.Context) ([]QuarantineItem, error) {
	var rows []entities.RedisUsageInbox
	if err := r.db.WithContext(ctx).Select("id,source_id,delivery_event_id,attempt_count").Where("source_id<>'' AND status IN ?", []string{"quarantined", "decode_failed", "discarded"}).Order("id").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	// 已删除的来源查不到名称，界面退回显示内部 ID。
	names, err := r.sources.SourceNames(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]QuarantineItem, 0, len(rows))
	for _, v := range rows {
		result = append(result, QuarantineItem{ID: v.ID, SourceID: v.SourceID, SourceName: names[v.SourceID], DeliveryEventID: v.DeliveryEventID, Attempts: v.AttemptCount})
	}
	return result, nil
}
func (r *Receiver) Retry(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Model(&entities.RedisUsageInbox{}).Where("id=? AND source_id<>'' AND status IN ?", id, []string{"quarantined", "decode_failed", "discarded"}).Updates(map[string]any{"status": "pending", "attempt_count": 0, "last_error": ""})
	return result.RowsAffected == 1, result.Error
}
