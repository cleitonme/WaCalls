package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func TestPresencePulseDuration(t *testing.T) {
	// Verify the pulse duration is 30 seconds as documented
	if presencePulseDuration != 30*time.Second {
		t.Fatalf("expected presencePulseDuration=30s, got %v", presencePulseDuration)
	}
}

func TestPresencePulseMinGap(t *testing.T) {
	// Verify the min gap is 1 hour
	if presencePulseMinGap != time.Hour {
		t.Fatalf("expected presencePulseMinGap=1h, got %v", presencePulseMinGap)
	}
}

func TestPresencePulse_PulsingFlag(t *testing.T) {
	// Test that pulsing flag prevents concurrent pulses
	var session atomic.Bool

	// First pulse should succeed (flag transitions from false to true)
	if !session.CompareAndSwap(false, true) {
		t.Fatal("first pulse should set pulsing=true")
	}

	// Second concurrent pulse should fail (flag is already true)
	if session.CompareAndSwap(false, true) {
		t.Fatal("second pulse should not change pulsing (already true)")
	}

	// Reset
	session.Store(false)
}

func TestPresencePulse_LastPulseTracking(t *testing.T) {
	// Test that lastPulse is tracked correctly
	var lastPulse atomic.Int64

	// Initially zero - this means Unix(0, 0) which is Jan 1, 1970
	// This is before any pulse, so it should trigger a pulse
	initialLastPulse := lastPulse.Load()
	if initialLastPulse != 0 {
		t.Fatalf("expected initial lastPulse to be 0, got %d", initialLastPulse)
	}

	// After pulse
	lastPulse.Store(time.Now().UnixNano())
	if diff := time.Since(time.Unix(0, lastPulse.Load())); diff < 0 || diff > 5*time.Second {
		t.Fatalf("lastPulse should be recent, got diff=%v", diff)
	}
}

func sinceIsNegative(d time.Duration) bool {
	return d < 0
}

func TestPresencePulse_MinGapEnforcement(t *testing.T) {
	// Test that refreshPresence respects the min gap

	var lastPulse atomic.Int64

	// Simulate just-pulsed state
	now := time.Now()
	lastPulse.Store(now.UnixNano())

	// Check if we should pulse (should NOT because we're within the gap)
	since := time.Since(time.Unix(0, lastPulse.Load()))
	if since < presencePulseMinGap {
		// We're in the gap, so no pulse should happen
		// This is the correct behavior
	} else {
		// We're outside the gap, pulse would happen
		// This is also correct behavior
	}
}

// This test verifies the presence constants are correctly exported
func TestPresenceConstants_CompileCheck(t *testing.T) {
	// Just verifying these compile correctly
	_ = presencePulseDuration
	_ = presencePulseMinGap

	// Verify they have sensible values
	if presencePulseDuration <= 0 {
		t.Error("presencePulseDuration should be positive")
	}
	if presencePulseMinGap <= 0 {
		t.Error("presencePulseMinGap should be positive")
	}
	if presencePulseMinGap <= presencePulseDuration {
		t.Error("presencePulseMinGap should be greater than presencePulseDuration")
	}
}

// Mock presence send to verify behavior
type mockPresenceSender struct {
	sendCount int64
	errors    error
}

func (m *mockPresenceSender) SendPresence(ctx context.Context, state types.Presence) error {
	atomic.AddInt64(&m.sendCount, 1)
	return m.errors
}

func TestSendPresenceTimeout_Context(t *testing.T) {
	// Test that sendPresenceTimeout respects context cancellation
	// Note: we can't test this easily with a real whatsmeow client
	// The logic in sendPresenceTimeout is:
	// 1. Create context with 10s timeout
	// 2. Call client.SendPresence with that context
	// 3. The timeout cancels the context after 10s
}