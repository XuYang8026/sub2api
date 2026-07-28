package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// <fork:models-list>
//
// Folds the models priced on the group's channel into the /v1/models listing,
// so a newly released model shows up as soon as it is priced instead of
// waiting for the hardcoded claude.DefaultModels table to be updated upstream.
// See internal/service/forkext_models_list.go for the lookup itself.

// forkMergeChannelModels merges the channel priced models into availableModels.
// Scoped to Anthropic: other platforms keep their upstream behavior.
func forkMergeChannelModels(ctx context.Context, availableModels []string, groupID *int64, platform string) []string {
	if groupID == nil || platform != service.PlatformAnthropic {
		return availableModels
	}
	extra := service.ForkChannelModelIDs(ctx, *groupID, platform)
	return forkApplyChannelModels(availableModels, extra, platform)
}

// forkApplyChannelModels merges extra into availableModels.
//
// When availableModels is empty — no account declares a model_mapping — the
// upstream caller falls back to the platform default table. Merging into the
// defaults preserves that fallback; assigning extra directly would drop every
// default model from the listing.
func forkApplyChannelModels(availableModels, extra []string, platform string) []string {
	if len(extra) == 0 {
		return availableModels
	}
	base := availableModels
	if len(base) == 0 {
		base = defaultModelIDsForPlatform(platform)
	}
	return mergeModelIDs(base, extra)
}

// </fork:models-list>
