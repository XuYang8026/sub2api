package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// <fork:models-list>

func TestForkChannelPricingModelIDs(t *testing.T) {
	tests := []struct {
		name     string
		channel  *Channel
		platform string
		want     []string
	}{
		{
			name:     "nil channel returns nothing",
			channel:  nil,
			platform: PlatformAnthropic,
			want:     nil,
		},
		{
			name: "collects models from matching platform",
			channel: &Channel{
				Status: StatusActive,
				ModelPricing: []ChannelModelPricing{
					{Platform: PlatformAnthropic, Models: []string{"claude-opus-5", "claude-sonnet-5"}},
				},
			},
			platform: PlatformAnthropic,
			want:     []string{"claude-opus-5", "claude-sonnet-5"},
		},
		{
			name: "skips entries from other platforms",
			channel: &Channel{
				Status: StatusActive,
				ModelPricing: []ChannelModelPricing{
					{Platform: PlatformAnthropic, Models: []string{"claude-opus-5"}},
					{Platform: PlatformOpenAI, Models: []string{"gpt-5.6"}},
				},
			},
			platform: PlatformAnthropic,
			want:     []string{"claude-opus-5"},
		},
		{
			name: "skips wildcard entries",
			channel: &Channel{
				Status: StatusActive,
				ModelPricing: []ChannelModelPricing{
					{Platform: PlatformAnthropic, Models: []string{"claude-*", "claude-opus-5"}},
				},
			},
			platform: PlatformAnthropic,
			want:     []string{"claude-opus-5"},
		},
		{
			name: "dedupes across pricing entries",
			channel: &Channel{
				Status: StatusActive,
				ModelPricing: []ChannelModelPricing{
					{Platform: PlatformAnthropic, Models: []string{"claude-opus-5"}},
					{Platform: PlatformAnthropic, Models: []string{"claude-opus-5", "claude-haiku-4-5"}},
				},
			},
			platform: PlatformAnthropic,
			want:     []string{"claude-opus-5", "claude-haiku-4-5"},
		},
		{
			name: "skips blank model names",
			channel: &Channel{
				Status: StatusActive,
				ModelPricing: []ChannelModelPricing{
					{Platform: PlatformAnthropic, Models: []string{"", "  ", "claude-opus-5"}},
				},
			},
			platform: PlatformAnthropic,
			want:     []string{"claude-opus-5"},
		},
		{
			name: "no pricing entries returns nothing",
			channel: &Channel{
				Status:       StatusActive,
				ModelPricing: nil,
			},
			platform: PlatformAnthropic,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forkChannelPricingModelIDs(tt.channel, tt.platform)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestForkMergeCandidateModels(t *testing.T) {
	t.Run("no channel models leaves candidates untouched", func(t *testing.T) {
		candidates := []string{"claude-opus-4-8"}
		require.Equal(t, candidates, forkMergeCandidateModels(candidates, nil))
	})

	t.Run("appends channel models", func(t *testing.T) {
		got := forkMergeCandidateModels([]string{"claude-opus-4-8"}, []string{"claude-opus-5"})
		require.Equal(t, []string{"claude-opus-4-8", "claude-opus-5"}, got)
	})

	t.Run("dedupes case insensitively", func(t *testing.T) {
		got := forkMergeCandidateModels([]string{"claude-opus-5"}, []string{"Claude-Opus-5", "claude-sonnet-5"})
		require.Equal(t, []string{"claude-opus-5", "claude-sonnet-5"}, got)
	})
}

func TestForkAppendChannelModelsGating(t *testing.T) {
	forkChannelServiceRef.Store(nil)

	t.Run("non anthropic platform is left untouched", func(t *testing.T) {
		candidates := []string{"gpt-5.6"}
		require.Equal(t, candidates, forkAppendChannelModels(t.Context(), candidates, 1, PlatformOpenAI))
	})

	t.Run("unregistered service leaves candidates untouched", func(t *testing.T) {
		candidates := []string{"claude-opus-4-8"}
		require.Equal(t, candidates, forkAppendChannelModels(t.Context(), candidates, 1, PlatformAnthropic))
	})
}

func TestForkChannelModelIDsWithoutRegisteredService(t *testing.T) {
	// The singleton is unset in unit tests; the lookup must degrade to empty
	// rather than panicking.
	forkChannelServiceRef.Store(nil)
	require.Empty(t, ForkChannelModelIDs(t.Context(), 1, PlatformAnthropic))
}

// </fork:models-list>
