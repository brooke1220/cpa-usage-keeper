package service

import (
	"context"
	"errors"
	"strings"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

var ErrInvalidID = errors.New("invalid id")

type CPAAPIKeyProvider interface {
	ListCPAAPIKeys(ctx context.Context) ([]entities.CPAAPIKey, error)
	FindActiveCPAAPIKeyByValue(ctx context.Context, apiKey string) (entities.CPAAPIKey, error)
	FindActiveCPAAPIKeyByID(ctx context.Context, id int64) (entities.CPAAPIKey, error)
	UpdateCPAAPIKeyAlias(ctx context.Context, id int64, keyAlias string) (entities.CPAAPIKey, error)
}

// CPAAPIKeyAliasImportMode 区分用户主动导入和设置页的后台自动导入。
type CPAAPIKeyAliasImportMode string

const (
	// CPAAPIKeyAliasImportManual 由管理员在导入弹窗中确认，填充全部空别名。
	CPAAPIKeyAliasImportManual CPAAPIKeyAliasImportMode = "manual"
	// CPAAPIKeyAliasImportAuto 来自同源浏览器存储的后台导入，跳过管理员手动清空的别名。
	CPAAPIKeyAliasImportAuto CPAAPIKeyAliasImportMode = "auto"
)

// CPAAPIKeyAliasImporter persists browser-local CPA names without exposing credentials.
// It is optional so read-only providers remain compatible with the API routes.
type CPAAPIKeyAliasImporter interface {
	ImportCPAAPIKeyAliases(ctx context.Context, aliases []entities.CPAAPIKeyAlias, mode CPAAPIKeyAliasImportMode) ([]entities.CPAAPIKey, error)
}

type cpaAPIKeyService struct {
	db *gorm.DB
}

func NewCPAAPIKeyService(db *gorm.DB) CPAAPIKeyProvider {
	return &cpaAPIKeyService{db: db}
}

func (s *cpaAPIKeyService) ListCPAAPIKeys(context.Context) ([]entities.CPAAPIKey, error) {
	return repository.ListActiveCPAAPIKeys(s.db)
}

// FindActiveCPAAPIKeyByValue 只用于 Key 自助登录：要求 Key 仍在拉取的 CPA 清单里，只经推送出现的 Key 不能登录。
func (s *cpaAPIKeyService) FindActiveCPAAPIKeyByValue(_ context.Context, apiKey string) (entities.CPAAPIKey, error) {
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return entities.CPAAPIKey{}, gorm.ErrRecordNotFound
	}
	return repository.FindLoginCPAAPIKeyByValue(s.db, trimmed)
}

// FindActiveCPAAPIKeyByID 校验 Key 查看会话，与登录使用同一可登录条件；Key 离开拉取清单后会话随即失效。
func (s *cpaAPIKeyService) FindActiveCPAAPIKeyByID(_ context.Context, id int64) (entities.CPAAPIKey, error) {
	if id <= 0 {
		return entities.CPAAPIKey{}, gorm.ErrRecordNotFound
	}
	return repository.FindLoginCPAAPIKeyByID(s.db, id)
}

func (s *cpaAPIKeyService) UpdateCPAAPIKeyAlias(_ context.Context, id int64, keyAlias string) (entities.CPAAPIKey, error) {
	if id <= 0 {
		return entities.CPAAPIKey{}, ErrInvalidID
	}
	// UPDATE 由 dbresolver 自动路由 writer；结果回读再用官方 Write clause 固定到同一物理池。
	if err := repository.UpdateCPAAPIKeyAlias(s.db, id, keyAlias); err != nil {
		return entities.CPAAPIKey{}, err
	}
	return repository.FindActiveCPAAPIKeyByID(s.db.Clauses(dbresolver.Write), id)
}

func (s *cpaAPIKeyService) ImportCPAAPIKeyAliases(ctx context.Context, aliases []entities.CPAAPIKeyAlias, mode CPAAPIKeyAliasImportMode) ([]entities.CPAAPIKey, error) {
	return repository.ImportCPAAPIKeyAliases(s.db.WithContext(ctx), aliases, mode == CPAAPIKeyAliasImportAuto)
}
