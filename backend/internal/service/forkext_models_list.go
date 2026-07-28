package service

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
)

// <fork:models-list>
//
// Sidecar wiring that lets the channel pricing table feed the /v1/models
// listing. Upstream derives the Anthropic model list from the hardcoded
// claude.DefaultModels table, so a newly released model only shows up after
// upstream edits that table. Operators already maintain the channel pricing
// entries for the models they serve, so those entries are a source of truth
// this deployment controls.
//
// adminServiceImpl does not hold a ChannelService, and adding the field would
// touch the upstream struct and its constructor. Following the existing fork
// convention (see GetProxyCircuitBreaker), the reference is kept as a package
// level singleton registered through wire instead.

var forkChannelServiceRef atomic.Pointer[ChannelService]

// ForkModelsListRegistration marks that the ChannelService singleton has been
// registered. It carries no data: it exists so wire can order the registration
// ahead of the components that depend on it.
type ForkModelsListRegistration struct{}

// ProvideForkModelsListRegistration registers the ChannelService singleton used
// by the /v1/models lookups.
func ProvideForkModelsListRegistration(channelService *ChannelService) ForkModelsListRegistration {
	RegisterForkChannelService(channelService)
	return ForkModelsListRegistration{}
}

// RegisterForkChannelService stores the ChannelService used to resolve channel
// pricing models. Registered from the fork wire provider set.
func RegisterForkChannelService(channelService *ChannelService) {
	if channelService == nil {
		return
	}
	forkChannelServiceRef.Store(channelService)
}

// ForkChannelModelIDs returns the concrete model IDs configured in the pricing
// table of the channel serving groupID, limited to the given platform.
// It returns nothing when the group has no active channel, when the lookup
// fails, or before the singleton is registered — the model listing must never
// fail because of this lookup.
func ForkChannelModelIDs(ctx context.Context, groupID int64, platform string) []string {
	channelService := forkChannelServiceRef.Load()
	if channelService == nil || groupID <= 0 {
		return nil
	}

	channel, err := channelService.GetChannelForGroup(ctx, groupID)
	if err != nil {
		slog.Debug("fork.models_list.channel_lookup_failed",
			"group_id", groupID,
			"error", err,
		)
		return nil
	}

	return forkChannelPricingModelIDs(channel, platform)
}

// forkAppendChannelModels adds the channel priced models to the admin side
// candidate pool, so they can be picked in a group's custom /v1/models list.
// Scoped to Anthropic: other platforms keep their upstream behavior.
func forkAppendChannelModels(ctx context.Context, candidates []string, groupID int64, platform string) []string {
	if platform != PlatformAnthropic {
		return candidates
	}
	return forkMergeCandidateModels(candidates, ForkChannelModelIDs(ctx, groupID, platform))
}

// forkMergeCandidateModels appends extra to candidates, skipping duplicates.
// Matching is case insensitive because channel pricing entries are matched that
// way at request time.
func forkMergeCandidateModels(candidates, extra []string) []string {
	if len(extra) == 0 {
		return candidates
	}

	seen := make(map[string]struct{}, len(candidates)+len(extra))
	for _, model := range candidates {
		seen[strings.ToLower(strings.TrimSpace(model))] = struct{}{}
	}
	for _, model := range extra {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, model)
	}
	return candidates
}

// forkChannelPricingModelIDs collects the model IDs the channel prices for the
// given platform. Wildcard entries such as "claude-*" are skipped: they are
// match patterns, not model IDs, so they cannot be listed.
func forkChannelPricingModelIDs(channel *Channel, platform string) []string {
	if channel == nil || platform == "" {
		return nil
	}

	var models []string
	seen := make(map[string]struct{})
	for i := range channel.ModelPricing {
		pricing := &channel.ModelPricing[i]
		if !strings.EqualFold(strings.TrimSpace(pricing.Platform), platform) {
			continue
		}
		for _, model := range pricing.Models {
			model = strings.TrimSpace(model)
			if model == "" || strings.HasSuffix(model, "*") {
				continue
			}
			key := strings.ToLower(model)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			models = append(models, model)
		}
	}
	return models
}

// </fork:models-list>
