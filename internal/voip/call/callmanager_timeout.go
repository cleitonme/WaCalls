package call

import (
	"context"
	"time"

	"wacalls/internal/voip/core"
)

type Timeouts struct {
	Ring            time.Duration
	Answer          time.Duration
	MediaConnect    time.Duration
	MediaInactivity time.Duration
	MaxDuration     time.Duration
}

var DefaultTimeouts = Timeouts{
	Ring:            60 * time.Second,
	Answer:          60 * time.Second,
	MediaConnect:    30 * time.Second,
	MediaInactivity: 30 * time.Second,
	MaxDuration:     4 * time.Hour,
}

const defaultWatchdogTick = time.Second

func (m *CallManager) startWatchdog() {
	m.mu.Lock()
	if m.watchdogStop != nil {
		close(m.watchdogStop)
	}
	stop := make(chan struct{})
	m.watchdogStop = stop
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(m.watchdogTick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.expireIfOverdue()
			}
		}
	}()
}

func (m *CallManager) expireIfOverdue() {
	m.mu.Lock()
	call := m.currentCall
	if call == nil || call.IsEnded() {
		if call != nil && call.IsEnded() && m.watchdogStop != nil {
			close(m.watchdogStop)
			m.watchdogStop = nil
		}
		m.mu.Unlock()
		return
	}
	deadline, ok := phaseDeadline(call, m.timeouts)
	state := call.StateData.State
	callID := call.CallID
	inactivity := m.timeouts.MediaInactivity
	lastMedia := m.lastMediaRecv
	m.mu.Unlock()

	if state == core.CallStateActive && inactivity > 0 && !lastMedia.IsZero() {
		if time.Since(lastMedia) > inactivity {
			m.log.Warn("media inactivity timeout — peer stopped sending audio", "call_id", callID, "idle", time.Since(lastMedia))
			_ = m.EndCall(context.Background(), core.EndCallReasonTimeout)
			return
		}
	}

	if !ok || time.Now().Before(deadline) {
		return
	}
	m.log.Info("call timed out", "call_id", callID, "state", string(state))
	_ = m.EndCall(context.Background(), core.EndCallReasonTimeout)
}

func phaseDeadline(c *CallInfo, t Timeouts) (time.Time, bool) {
	s := c.StateData
	switch s.State {
	case core.CallStateInitiating, core.CallStateRinging:
		if t.Answer <= 0 {
			return time.Time{}, false
		}
		return c.CreatedAt.Add(t.Answer), true
	case core.CallStateIncomingRinging:
		if t.Ring <= 0 {
			return time.Time{}, false
		}
		return c.CreatedAt.Add(t.Ring), true
	case core.CallStateConnecting:
		if t.MediaConnect <= 0 || s.AcceptedAt == nil {
			return time.Time{}, false
		}
		return s.AcceptedAt.Add(t.MediaConnect), true
	case core.CallStateActive, core.CallStateOnHold:
		if t.MaxDuration <= 0 || s.ConnectedAt == nil {
			return time.Time{}, false
		}
		return s.ConnectedAt.Add(t.MaxDuration), true
	default:
		return time.Time{}, false
	}
}
