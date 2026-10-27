package ingest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/timeutil"
	"gorm.io/gorm"
)

// EnabledSettingKey 是推送接收开关在 app_settings 中的键；缺省为关闭，管理员在设置页打开。
const EnabledSettingKey = "ingest.push_enabled"

const (
	maxSourceNameRunes = 64
	tokenBytes         = 32
	tokenHintRunes     = 4
	// lastUsedFlushInterval 限制“最近上传”写库频率；每批上传都写一次会让唯一 writer 多一倍事务。
	lastUsedFlushInterval = time.Minute
)

var (
	ErrSourceNotFound    = errors.New("push source not found")
	ErrInvalidSourceName = errors.New("push source name must contain 1-64 characters")
	// ErrSourceActive 防止误删仍在使用的来源：必须先吊销，确认 CPA 已停用后再删除。
	ErrSourceActive = errors.New("revoke the push source before deleting it")
)

// Source 是返回给管理员的来源信息，从不包含 token 明文或完整哈希。
type Source struct {
	ID         int64      `json:"id"`
	SourceID   string     `json:"source_id"`
	Name       string     `json:"name"`
	TokenHint  string     `json:"token_hint"`
	Revoked    bool       `json:"revoked"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// IssuedSource 只在创建或更换 token 的响应里出现一次。
type IssuedSource struct {
	Source
	Token string `json:"token"`
}

type sourceCredential struct {
	id       int64
	sourceID string
	hash     [sha256.Size]byte
}

// sourceSnapshot 是接收请求读取的不可变视图；任何修改都整体换一个新快照。
type sourceSnapshot struct {
	enabled bool
	active  []sourceCredential
}

// Sources 管理设置页登记的推送来源。接收路径只读内存快照，不为每次上传查库。
type Sources struct {
	db       *gorm.DB
	now      func() time.Time
	snapshot atomic.Pointer[sourceSnapshot]
	// mu 串行化所有修改，保证数据库与快照按同一顺序更新。
	mu sync.Mutex

	// usedMu 保护 recorded：每个来源最近一次写入 last_used_at 的时间，用于把写库合并到每分钟一次。
	usedMu   sync.Mutex
	recorded map[int64]time.Time
}

func NewSources(ctx context.Context, db *gorm.DB) (*Sources, error) {
	if db == nil {
		return nil, fmt.Errorf("push sources database is required")
	}
	s := &Sources{db: db, now: time.Now, recorded: map[int64]time.Time{}}
	if err := s.reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// reload 从数据库重建快照。修改已提交而重载失败时必须失败关闭：保留旧快照会让刚吊销或更换的 token 继续通过。
// 换成全拒的空快照后，插件收到 401 会保留数据重试，下一次成功的设置操作或重启会恢复接收。
func (s *Sources) reload(ctx context.Context) error {
	if err := s.loadSnapshot(ctx); err != nil {
		s.snapshot.Store(&sourceSnapshot{})
		return err
	}
	return nil
}

func (s *Sources) loadSnapshot(ctx context.Context) error {
	setting, found, err := repository.GetAppSetting(ctx, s.db, EnabledSettingKey)
	if err != nil {
		return fmt.Errorf("load push ingest setting: %w", err)
	}
	enabled := found && setting.Value != nil && *setting.Value == "true"
	var rows []entities.PushSource
	if err := s.db.WithContext(ctx).Where("revoked_at IS NULL").Order("id").Find(&rows).Error; err != nil {
		return fmt.Errorf("load push sources: %w", err)
	}
	next := &sourceSnapshot{enabled: enabled, active: make([]sourceCredential, 0, len(rows))}
	for _, row := range rows {
		raw, err := hex.DecodeString(row.TokenSHA256)
		if err != nil || len(raw) != sha256.Size {
			return fmt.Errorf("push source %d has an invalid token hash", row.ID)
		}
		credential := sourceCredential{id: row.ID, sourceID: row.SourceID}
		copy(credential.hash[:], raw)
		next.active = append(next.active, credential)
	}
	s.snapshot.Store(next)
	return nil
}

// Enabled 报告推送接收开关；关闭时接收接口返回 503，插件保留数据稍后补传。
func (s *Sources) Enabled() bool {
	if s == nil {
		return false
	}
	snapshot := s.snapshot.Load()
	return snapshot != nil && snapshot.enabled
}

// authenticate 返回 token 对应的内部来源 ID。逐个常量时间比较且不提前退出，响应时间不暴露命中位置。
func (s *Sources) authenticate(token string) (int64, string, bool) {
	snapshot := s.snapshot.Load()
	if snapshot == nil {
		return 0, "", false
	}
	hash := sha256.Sum256([]byte(token))
	var id int64
	sourceID := ""
	for _, credential := range snapshot.active {
		if subtle.ConstantTimeCompare(hash[:], credential.hash[:]) == 1 {
			id, sourceID = credential.id, credential.sourceID
		}
	}
	return id, sourceID, sourceID != ""
}

// shouldRecordUse 判断这次上传是否需要更新 last_used_at；同一来源每分钟最多写一次，避免每批上传都多一次写入。
func (s *Sources) shouldRecordUse(id int64, at time.Time) bool {
	s.usedMu.Lock()
	defer s.usedMu.Unlock()
	if last, ok := s.recorded[id]; ok && at.Sub(last) < lastUsedFlushInterval {
		return false
	}
	s.recorded[id] = at
	return true
}

// forgetRecordedUse 在接收事务回滚后撤回合并记录，下一次成功上传会重新写入。
func (s *Sources) forgetRecordedUse(id int64, at time.Time) {
	s.usedMu.Lock()
	defer s.usedMu.Unlock()
	if s.recorded[id].Equal(at) {
		delete(s.recorded, id)
	}
}

func (s *Sources) List(ctx context.Context) ([]Source, error) {
	var rows []entities.PushSource
	if err := s.db.WithContext(ctx).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]Source, 0, len(rows))
	for _, row := range rows {
		result = append(result, toSource(row))
	}
	return result, nil
}

func (s *Sources) SetEnabled(ctx context.Context, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := strconv.FormatBool(enabled)
	if _, err := repository.UpsertAppSetting(ctx, s.db, entities.AppSetting{SettingKey: EnabledSettingKey, Value: &value, ValueType: entities.AppSettingValueTypeBool}); err != nil {
		return errors.Join(err, s.reloadAfterChange(ctx))
	}
	return s.reloadAfterChange(ctx)
}

func (s *Sources) Create(ctx context.Context, name string) (IssuedSource, error) {
	name, err := normalizeSourceName(name)
	if err != nil {
		return IssuedSource{}, err
	}
	token, hash, err := newToken()
	if err != nil {
		return IssuedSource{}, err
	}
	sourceID, err := newSourceID()
	if err != nil {
		return IssuedSource{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := entities.PushSource{SourceID: sourceID, Name: name, TokenSHA256: hash, TokenHint: tokenHint(token)}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return IssuedSource{}, errors.Join(fmt.Errorf("create push source: %w", err), s.reloadAfterChange(ctx))
	}
	if err := s.reloadAfterChange(ctx); err != nil {
		return IssuedSource{}, err
	}
	return IssuedSource{Source: toSource(row), Token: token}, nil
}

func (s *Sources) Rename(ctx context.Context, id int64, name string) (Source, error) {
	name, err := normalizeSourceName(name)
	if err != nil {
		return Source{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(ctx, id, map[string]any{"name": name})
}

// Rotate 换发新 token，旧 token 立即失效；SourceID 不变，CPA 换上新 token 后待传数据按原来源补齐。
func (s *Sources) Rotate(ctx context.Context, id int64) (IssuedSource, error) {
	token, hash, err := newToken()
	if err != nil {
		return IssuedSource{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.update(ctx, id, map[string]any{"token_sha256": hash, "token_hint": tokenHint(token)})
	if err != nil {
		return IssuedSource{}, err
	}
	return IssuedSource{Source: source, Token: token}, nil
}

func (s *Sources) Revoke(ctx context.Context, id int64) (Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(ctx, id, map[string]any{"revoked_at": timeutil.NormalizeStorageTime(s.now())})
}

func (s *Sources) Restore(ctx context.Context, id int64) (Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(ctx, id, map[string]any{"revoked_at": nil})
}

// Delete 只删除已吊销的来源登记；已入库的用量、去重凭据和待处理数据全部保留。
func (s *Sources) Delete(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var row entities.PushSource
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrSourceNotFound
		}
		return err
	}
	if row.RevokedAt == nil {
		return ErrSourceActive
	}
	if err := s.db.WithContext(ctx).Delete(&entities.PushSource{}, id).Error; err != nil {
		return errors.Join(err, s.reloadAfterChange(ctx))
	}
	return s.reloadAfterChange(ctx)
}

func (s *Sources) update(ctx context.Context, id int64, fields map[string]any) (Source, error) {
	result := s.db.WithContext(ctx).Model(&entities.PushSource{}).Where("id = ?", id).Updates(fields)
	if result.Error != nil {
		// 写入可能已在数据库提交后才报错（例如请求被取消），仍按数据库重建快照。
		return Source{}, errors.Join(result.Error, s.reloadAfterChange(ctx))
	}
	if result.RowsAffected == 0 {
		return Source{}, ErrSourceNotFound
	}
	// 修改已提交：无论后续回读成败都先重载，吊销或换发的 token 必须立即失效。
	if err := s.reloadAfterChange(ctx); err != nil {
		return Source{}, err
	}
	var row entities.PushSource
	if err := s.db.WithContext(context.WithoutCancel(ctx)).First(&row, id).Error; err != nil {
		return Source{}, err
	}
	return toSource(row), nil
}

// reloadAfterChange 在修改提交后重建快照。管理员关闭页面会取消请求，但修改已经生效，
// 重载不能随之放弃，否则吊销的 token 继续通过，或开关在内存中被误判为关闭。
func (s *Sources) reloadAfterChange(ctx context.Context) error {
	return s.reload(context.WithoutCancel(ctx))
}

// SourceNames 返回内部来源 ID 到显示名的映射，供状态面板和隔离列表展示。
func (s *Sources) SourceNames(ctx context.Context) (map[string]string, error) {
	var rows []entities.PushSource
	if err := s.db.WithContext(ctx).Select("source_id, name").Find(&rows).Error; err != nil {
		return nil, err
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.SourceID] = row.Name
	}
	return names, nil
}

func toSource(row entities.PushSource) Source {
	return Source{ID: row.ID, SourceID: row.SourceID, Name: row.Name, TokenHint: row.TokenHint, Revoked: row.RevokedAt != nil, RevokedAt: row.RevokedAt, LastUsedAt: row.LastUsedAt, CreatedAt: row.CreatedAt}
}

func normalizeSourceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxSourceNameRunes || strings.ContainsAny(name, "\r\n\t") {
		return "", ErrInvalidSourceName
	}
	return name, nil
}

func newToken() (string, string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate push token: %w", err)
	}
	token := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(hash[:]), nil
}

func newSourceID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate push source ID: %w", err)
	}
	return "src_" + hex.EncodeToString(raw), nil
}

func tokenHint(token string) string {
	if len(token) <= tokenHintRunes {
		return token
	}
	return token[len(token)-tokenHintRunes:]
}
