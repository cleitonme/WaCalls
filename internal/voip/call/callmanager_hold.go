package call

import (
	"math"
	"time"

	"wacalls/internal/voip/core"
)

// HoldCall puts the call in held state and starts injecting MOH audio to the peer.
// If mohURL is empty the keepalive goroutine fills the stream with silence automatically.
func (m *CallManager) HoldCall(mohURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.currentCall == nil {
		return &CallError{"no active call"}
	}
	if m.currentCall.StateData.State != core.CallStateActive {
		return &CallError{"call is not in active state"}
	}
	if err := m.currentCall.ApplyTransition(Transition{Type: TransitionHold}); err != nil {
		return err
	}
	m.onHold = true
	m.emitState()

	stop := make(chan struct{})
	m.mohStop = stop
	go m.runMOH(stop)

	return nil
}

// UnholdCall resumes normal audio bridging.
func (m *CallManager) UnholdCall() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.currentCall == nil {
		return &CallError{"no active call"}
	}
	if m.currentCall.StateData.State != core.CallStateOnHold {
		return &CallError{"call is not in held state"}
	}
	if err := m.currentCall.ApplyTransition(Transition{Type: TransitionResume}); err != nil {
		return err
	}
	m.onHold = false
	if m.mohStop != nil {
		close(m.mohStop)
		m.mohStop = nil
	}
	m.emitState()
	return nil
}

// runMOH injects a 440 Hz hold tone into the SRTP stream every 60 ms while on hold.
// The tone keeps the peer's media path alive and signals waiting status.
// When a real moh_url is provided the caller may extend this to decode MP3 via
// github.com/hajimehoshi/go-mp3 — for now the built-in tone is always used.
func (m *CallManager) runMOH(stop <-chan struct{}) {
	const (
		sampleRate = 16000
		frameSize  = 960 // 60 ms @ 16 kHz
		toneHz     = 440.0
		volume     = 0.25
	)

	frame := make([]float32, frameSize)
	phase := 0.0
	delta := 2 * math.Pi * toneHz / sampleRate
	for i := range frame {
		frame[i] = float32(math.Sin(phase) * volume)
		phase += delta
		if phase >= 2*math.Pi {
			phase -= 2 * math.Pi
		}
	}

	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.mu.Lock()
			ready := m.onHold &&
				m.codec != nil &&
				m.rtpSession != nil &&
				m.srtpSession != nil &&
				m.relay.HasConnection()
			if ready {
				// Update lastCaptureAt so the silence-keepalive goroutine stays idle.
				m.lastCaptureAt = time.Now()
				if opus, err := m.codec.Encode(frame); err == nil {
					m.sendOpusFrameLocked(opus)
				}
			}
			m.mu.Unlock()
		}
	}
}
