package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// <fork:models-list>

func TestForkApplyChannelModels(t *testing.T) {
	t.Run("no channel models leaves the list untouched", func(t *testing.T) {
		available := []string{"claude-opus-4-8"}
		got := forkApplyChannelModels(available, nil, service.PlatformAnthropic)
		require.Equal(t, available, got)
	})

	t.Run("appends channel models to the account derived list", func(t *testing.T) {
		got := forkApplyChannelModels(
			[]string{"claude-opus-4-8"},
			[]string{"claude-opus-5"},
			service.PlatformAnthropic,
		)
		require.Equal(t, []string{"claude-opus-4-8", "claude-opus-5"}, got)
	})

	t.Run("dedupes models already present", func(t *testing.T) {
		got := forkApplyChannelModels(
			[]string{"claude-opus-5"},
			[]string{"claude-opus-5", "claude-sonnet-5"},
			service.PlatformAnthropic,
		)
		require.Equal(t, []string{"claude-opus-5", "claude-sonnet-5"}, got)
	})

	// Regression guard: when no account declares a model_mapping the available
	// list is empty and upstream falls back to the hardcoded default table.
	// Overwriting it with the channel models alone would drop every default
	// model from /v1/models.
	t.Run("keeps the default table when the available list is empty", func(t *testing.T) {
		got := forkApplyChannelModels(nil, []string{"claude-opus-5"}, service.PlatformAnthropic)

		require.Contains(t, got, "claude-opus-5")
		for _, want := range defaultModelIDsForPlatform(service.PlatformAnthropic) {
			require.Contains(t, got, want)
		}
	})
}

func TestForkMergeChannelModelsGating(t *testing.T) {
	groupID := int64(1)

	t.Run("non anthropic platform is left untouched", func(t *testing.T) {
		available := []string{"gpt-5.6"}
		got := forkMergeChannelModels(t.Context(), available, &groupID, service.PlatformOpenAI)
		require.Equal(t, available, got)
	})

	t.Run("missing group is left untouched", func(t *testing.T) {
		available := []string{"claude-opus-4-8"}
		got := forkMergeChannelModels(t.Context(), available, nil, service.PlatformAnthropic)
		require.Equal(t, available, got)
	})
}

// </fork:models-list>
