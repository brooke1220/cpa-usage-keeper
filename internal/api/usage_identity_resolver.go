package api

import (
	"strings"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
	servicedto "cpa-usage-keeper/internal/service/dto"
)

type usageIdentityResolver struct {
	credentialsByIdentity map[string]entities.UsageIdentity
	providersByIdentity   map[string]entities.UsageIdentity
	pushSourceNames       map[string]string
	pushedByIdentity      map[string]entities.PushUsageIdentity
}

// resolveEvent keeps remote identities outside local credential matching and management.
func (r usageIdentityResolver) resolveEvent(row servicedto.UsageEventRecord) (resolvedUsageIdentity, bool) {
	if row.SourceID == "" {
		return r.resolveByAuthIndex(row.AuthIndex)
	}
	result := resolvedUsageIdentity{DisplayName: strings.TrimSpace(row.Provider), Type: strings.TrimSpace(row.Provider)}
	if item, ok := r.pushedByIdentity[row.AuthIndex]; ok && item.SourceID == row.SourceID {
		result.DisplayName = item.DisplayName
		if item.ProviderType != "" {
			result.Type = item.ProviderType
		}
	}
	if result.DisplayName == "" {
		result.DisplayName = r.pushSourceNames[row.SourceID]
	}
	if result.DisplayName == "" {
		result.DisplayName = row.SourceID
	}
	return result, true
}

func newUsageIdentityResolver(identities []entities.UsageIdentity) usageIdentityResolver {
	credentialsByIdentity := make(map[string]entities.UsageIdentity, len(identities))
	providersByIdentity := make(map[string]entities.UsageIdentity, len(identities))
	for _, identity := range identities {
		if identity.IsDeleted {
			continue
		}
		key := strings.TrimSpace(identity.Identity)
		if key == "" {
			continue
		}
		switch identity.AuthType {
		case entities.UsageIdentityAuthTypeCredential:
			credentialsByIdentity[key] = identity
		case entities.UsageIdentityAuthTypeAIProvider:
			providersByIdentity[key] = identity
		}
	}

	return usageIdentityResolver{
		credentialsByIdentity: credentialsByIdentity,
		providersByIdentity:   providersByIdentity,
	}
}

type resolvedUsageIdentity struct {
	DisplayName string
	Type        string
}

func resolvedUsageIdentityFromEntity(item entities.UsageIdentity) resolvedUsageIdentity {
	return resolvedUsageIdentity{
		DisplayName: helper.UsageIdentityDisplayName(item),
		Type:        strings.TrimSpace(item.Type),
	}
}

func (r usageIdentityResolver) resolveByAuthIndex(authIndex string) (resolvedUsageIdentity, bool) {
	key := strings.TrimSpace(authIndex)
	if key == "" {
		return resolvedUsageIdentity{}, false
	}
	if identity, ok := r.providersByIdentity[key]; ok {
		return resolvedUsageIdentityFromEntity(identity), true
	}
	if identity, ok := r.credentialsByIdentity[key]; ok {
		return resolvedUsageIdentityFromEntity(identity), true
	}
	return resolvedUsageIdentity{}, false
}
