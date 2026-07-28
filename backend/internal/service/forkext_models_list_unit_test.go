//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// <fork:models-list>
//
// End-to-end coverage over a real ChannelService. Lives behind the unit tag
// because mockChannelRepository / newTestChannelService are defined in
// channel_service_test.go, which is unit tagged.

func TestForkChannelModelIDsFromRegisteredService(t *testing.T) {
	const groupID = int64(42)

	repo := &mockChannelRepository{
		listAllFn: func(context.Context) ([]Channel, error) {
			return []Channel{{
				ID:       7,
				Status:   StatusActive,
				GroupIDs: []int64{groupID},
				ModelPricing: []ChannelModelPricing{
					{Platform: PlatformAnthropic, Models: []string{"claude-opus-5", "claude-*"}},
					{Platform: PlatformOpenAI, Models: []string{"gpt-5.6"}},
				},
			}}, nil
		},
	}

	RegisterForkChannelService(newTestChannelService(repo))
	t.Cleanup(func() { forkChannelServiceRef.Store(nil) })

	t.Run("returns anthropic models for the group", func(t *testing.T) {
		require.Equal(t,
			[]string{"claude-opus-5"},
			ForkChannelModelIDs(t.Context(), groupID, PlatformAnthropic),
		)
	})

	t.Run("returns nothing for a group without a channel", func(t *testing.T) {
		require.Empty(t, ForkChannelModelIDs(t.Context(), 999, PlatformAnthropic))
	})

	t.Run("candidate pool picks up the channel models", func(t *testing.T) {
		got := forkAppendChannelModels(t.Context(), []string{"claude-opus-4-8"}, groupID, PlatformAnthropic)
		require.Equal(t, []string{"claude-opus-4-8", "claude-opus-5"}, got)
	})
}

// </fork:models-list>
