package api

import (
	"context"
	"sort"
	"strings"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/ingest"
	"cpa-usage-keeper/internal/service"
	servicedto "cpa-usage-keeper/internal/service/dto"
	"github.com/gin-gonic/gin"
)

const pushSourceFilterPrefix = "push-source:"

// loadUsageEventResolver 为导出和筛选项加载全部推送名称；列表页只查当前页，见 loadUsageEventPageResolver。
func loadUsageEventResolver(c *gin.Context, local service.UsageIdentityProvider, sources *ingest.Sources) (usageIdentityResolver, error) {
	resolver, err := loadLocalUsageEventResolver(c, local)
	if err != nil || sources == nil {
		return resolver, err
	}
	pushed, err := sources.UsageIdentities(c.Request.Context())
	if err != nil {
		return resolver, err
	}
	return withPushedUsageNames(c.Request.Context(), resolver, sources, pushed)
}

// loadUsageEventPageResolver 只为当前页出现的推送凭据查名称；整页都是本机事件时不读推送目录。
func loadUsageEventPageResolver(c *gin.Context, local service.UsageIdentityProvider, sources *ingest.Sources, rows []servicedto.UsageEventRecord) (usageIdentityResolver, error) {
	resolver, err := loadLocalUsageEventResolver(c, local)
	if err != nil || sources == nil {
		return resolver, err
	}
	hasPushedRows := false
	authIndexes := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.SourceID == "" {
			continue
		}
		hasPushedRows = true
		if row.AuthIndex != "" {
			authIndexes = append(authIndexes, row.AuthIndex)
		}
	}
	if !hasPushedRows {
		return resolver, nil
	}
	pushed, err := sources.UsageIdentitiesFor(c.Request.Context(), authIndexes)
	if err != nil {
		return resolver, err
	}
	return withPushedUsageNames(c.Request.Context(), resolver, sources, pushed)
}

func loadLocalUsageEventResolver(c *gin.Context, local service.UsageIdentityProvider) (usageIdentityResolver, error) {
	identities, err := loadUsageResolutionData(c, local)
	if err != nil {
		return usageIdentityResolver{}, err
	}
	return newUsageIdentityResolver(identities), nil
}

// withPushedUsageNames 附加 CPA 名称和远端凭据名称；push_sources 每台 CPA 一行，整表读取。
func withPushedUsageNames(ctx context.Context, resolver usageIdentityResolver, sources *ingest.Sources, pushed []entities.PushUsageIdentity) (usageIdentityResolver, error) {
	names, err := sources.SourceNames(ctx)
	if err != nil {
		return resolver, err
	}
	resolver.pushSourceNames = names
	resolver.pushedByIdentity = make(map[string]entities.PushUsageIdentity, len(pushed))
	for _, item := range pushed {
		resolver.pushedByIdentity[item.AuthIndex] = item
	}
	return resolver, nil
}

func pushedUsageSourceFilterOptions(resolver usageIdentityResolver) []usageSourceFilterOption {
	options := make([]usageSourceFilterOption, 0, len(resolver.pushSourceNames)+len(resolver.pushedByIdentity))
	for id, name := range resolver.pushSourceNames {
		options = append(options, usageSourceFilterOption{Value: pushSourceFilterPrefix + id, Label: name, DisplayName: name})
	}
	for index, item := range resolver.pushedByIdentity {
		name := resolver.pushSourceNames[item.SourceID]
		if name == "" {
			name = item.SourceID
		}
		label := name + " / " + strings.TrimSpace(item.DisplayName)
		options = append(options, usageSourceFilterOption{Value: index, Label: label, DisplayName: label})
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Label == options[j].Label {
			return options[i].Value < options[j].Value
		}
		return options[i].Label < options[j].Label
	})
	return options
}
