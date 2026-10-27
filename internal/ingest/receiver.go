// Package ingest receives authenticated, idempotent usage batches from CPA plugins.
package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
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

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
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

type source struct {
	id   string
	hash [32]byte
}
type Receiver struct {
	db      *gorm.DB
	sources []source
}

func New(db *gorm.DB, sources []config.PushSource) (*Receiver, error) {
	if db == nil {
		return nil, fmt.Errorf("push database is required")
	}
	if err := config.ValidatePushSources(sources); err != nil {
		return nil, err
	}
	r := &Receiver{db: db}
	for _, v := range sources {
		hash, _ := hex.DecodeString(v.TokenSHA256)
		s := source{id: v.SourceID}
		copy(s.hash[:], hash)
		r.sources = append(r.sources, s)
	}
	return r, nil
}

func (r *Receiver) authenticate(header string) string {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || len(token) < 32 || len(token) > 4096 {
		return ""
	}
	hash := sha256.Sum256([]byte(token))
	id := ""
	for _, s := range r.sources {
		if subtle.ConstantTimeCompare(hash[:], s.hash[:]) == 1 {
			id = s.id
		}
	}
	return id
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	sourceID := r.authenticate(req.Header.Get("Authorization"))
	if sourceID == "" {
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
	if b.SourceID != sourceID {
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
	ack, err := r.Accept(req.Context(), sourceID, b.Events, time.Now())
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
	ack := Ack{SchemaVersion: 1, AcceptedIDs: make([]string, 0, len(events))}
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
			key := strings.TrimSpace(payload.APIKey)
			if key != "" {
				record := entities.CPAAPIKey{APIKey: key, DisplayKey: helper.RedactSensitiveValue(key), LastSyncedAt: &now}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "api_key"}}, DoUpdates: clause.Assignments(map[string]any{"is_deleted": false, "last_synced_at": timeutil.NormalizeStorageTime(now)})}).Create(&record).Error; err != nil {
					return err
				}
			}
			ack.AcceptedIDs = append(ack.AcceptedIDs, e.ID)
		}
		return nil
	})
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
	LastReceivedAt *time.Time `json:"last_received_at,omitempty"`
	Pending        int64      `json:"pending"`
	Quarantined    int64      `json:"quarantined"`
}

// Status is polled by every open dashboard. Receipts are retained forever, so it never counts
// them and the inbox counts only cover unfinished rows. Receipts are never deleted and all
// ingest writes are serialized, so rowid order is receipt order; received_at text carries a
// local UTC offset and stops sorting chronologically when that offset changes.
func (r *Receiver) Status(ctx context.Context) (Status, error) {
	var s Status
	var latest entities.PushReceipt
	if err := r.db.WithContext(ctx).Select("source_id,received_at").Order("rowid DESC").Limit(1).Find(&latest).Error; err != nil {
		return s, err
	}
	if latest.SourceID != "" {
		v := latest.ReceivedAt
		s.LastReceivedAt = &v
	}
	if err := r.db.WithContext(ctx).Model(&entities.RedisUsageInbox{}).Where("source_id<>'' AND status IN ?", []string{"pending", "process_failed"}).Count(&s.Pending).Error; err != nil {
		return s, err
	}
	err := r.db.WithContext(ctx).Model(&entities.RedisUsageInbox{}).Where("source_id<>'' AND status IN ?", []string{"quarantined", "decode_failed", "discarded"}).Count(&s.Quarantined).Error
	return s, err
}

type QuarantineItem struct {
	ID              int64  `json:"id"`
	SourceID        string `json:"source_id"`
	DeliveryEventID string `json:"delivery_event_id"`
	Attempts        int    `json:"attempts"`
}

func (r *Receiver) Quarantine(ctx context.Context) ([]QuarantineItem, error) {
	var rows []entities.RedisUsageInbox
	err := r.db.WithContext(ctx).Select("id,source_id,delivery_event_id,attempt_count").Where("source_id<>'' AND status IN ?", []string{"quarantined", "decode_failed", "discarded"}).Order("id").Limit(100).Find(&rows).Error
	result := make([]QuarantineItem, 0, len(rows))
	for _, v := range rows {
		result = append(result, QuarantineItem{v.ID, v.SourceID, v.DeliveryEventID, v.AttemptCount})
	}
	return result, err
}
func (r *Receiver) Retry(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Model(&entities.RedisUsageInbox{}).Where("id=? AND source_id<>'' AND status IN ?", id, []string{"quarantined", "decode_failed", "discarded"}).Updates(map[string]any{"status": "pending", "attempt_count": 0, "last_error": ""})
	return result.RowsAffected == 1, result.Error
}
