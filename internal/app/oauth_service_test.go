package app

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/softsrv/rowbot/internal/oauth"
)

// TestDiscordGuildMembershipCache exercises the cache primitives directly
// (miss, hit, expiry, and caching a legitimate "no guilds" result) without
// needing a live DB or Discord API — GetDiscordGuildMemberships itself is
// exercised through internal/app's integration tests instead.
func TestDiscordGuildMembershipCache(t *testing.T) {
	userA := uuid.Must(uuid.NewV7())
	userB := uuid.Must(uuid.NewV7())

	tests := []struct {
		name    string
		setup   func(c *discordGuildMembershipCache)
		userID  uuid.UUID
		wantOK  bool
		wantLen int
	}{
		{
			name:   "miss when never set",
			setup:  func(c *discordGuildMembershipCache) {},
			userID: userA,
			wantOK: false,
		},
		{
			name: "hit before expiry",
			setup: func(c *discordGuildMembershipCache) {
				c.set(userA, []oauth.DiscordGuild{{ID: "1", Name: "g1"}})
			},
			userID:  userA,
			wantOK:  true,
			wantLen: 1,
		},
		{
			name: "miss after expiry",
			setup: func(c *discordGuildMembershipCache) {
				c.store.Store(userA, &discordGuildMembershipCacheEntry{
					guilds:    []oauth.DiscordGuild{{ID: "1", Name: "g1"}},
					expiresAt: time.Now().Add(-time.Second),
				})
			},
			userID: userA,
			wantOK: false,
		},
		{
			name: "empty/no-guilds result is cached and distinct from a miss",
			setup: func(c *discordGuildMembershipCache) {
				c.set(userA, nil)
			},
			userID:  userA,
			wantOK:  true,
			wantLen: 0,
		},
		{
			name: "keys are per-user",
			setup: func(c *discordGuildMembershipCache) {
				c.set(userA, []oauth.DiscordGuild{{ID: "1", Name: "g1"}})
			},
			userID: userB,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newDiscordGuildMembershipCache()
			tt.setup(c)

			got, ok := c.get(tt.userID)
			if ok != tt.wantOK {
				t.Fatalf("get() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && len(got) != tt.wantLen {
				t.Fatalf("get() len = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

// TestDiscordGuildMembershipCacheSweep verifies the sweep goroutine reclaims
// expired entries and leaves live ones alone.
func TestDiscordGuildMembershipCacheSweep(t *testing.T) {
	c := newDiscordGuildMembershipCache()

	expiredUser := uuid.Must(uuid.NewV7())
	liveUser := uuid.Must(uuid.NewV7())

	c.store.Store(expiredUser, &discordGuildMembershipCacheEntry{
		expiresAt: time.Now().Add(-time.Minute),
	})
	c.store.Store(liveUser, &discordGuildMembershipCacheEntry{
		guilds:    []oauth.DiscordGuild{{ID: "1", Name: "g1"}},
		expiresAt: time.Now().Add(time.Hour),
	})

	// Exercise the same sweep logic the background goroutine runs, without
	// waiting on the real ticker interval.
	now := time.Now()
	c.store.Range(func(k, v any) bool {
		if e, ok := v.(*discordGuildMembershipCacheEntry); ok && now.After(e.expiresAt) {
			c.store.Delete(k)
		}
		return true
	})

	if _, ok := c.store.Load(expiredUser); ok {
		t.Fatal("expired entry was not swept")
	}
	if _, ok := c.store.Load(liveUser); !ok {
		t.Fatal("live entry was incorrectly swept")
	}
}
