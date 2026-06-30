package call

import (
	"encoding/binary"
	"math"
	"os/exec"
	"strconv"
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
	go m.runMOH(stop, mohURL)

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

// runMOH injects audio into the SRTP stream every 60 ms while on hold.
// If mohURL is provided it decodes the MP3 via ffmpeg and loops the audio.
// Falls back to a 440 Hz tone when ffmpeg is unavailable or mohURL is empty.
func (m *CallManager) runMOH(stop <-chan struct{}, mohURL string) {
	const (
		sampleRate = 16000
		frameSize  = 960 // 60 ms @ 16 kHz
	)

	var pcmBuffer []float32
	if mohURL != "" {
		if buf, err := decodeMOHViaffmpeg(mohURL, sampleRate); err == nil && len(buf) > 0 {
			pcmBuffer = buf
		} else {
			m.log.Warn("MOH: ffmpeg decode failed, falling back to tone", "url", mohURL, "err", err)
		}
	}

	// Build tone fallback.
	tone := make([]float32, frameSize)
	phase := 0.0
	delta := 2 * math.Pi * 440.0 / sampleRate
	for i := range tone {
		tone[i] = float32(math.Sin(phase) * 0.25)
		phase += delta
		if phase >= 2*math.Pi {
			phase -= 2 * math.Pi
		}
	}

	bufPos := 0
	frame := make([]float32, frameSize)

	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if len(pcmBuffer) > 0 {
				for i := range frame {
					frame[i] = pcmBuffer[bufPos]
					bufPos = (bufPos + 1) % len(pcmBuffer)
				}
			} else {
				copy(frame, tone)
			}

			m.mu.Lock()
			ready := m.onHold &&
				m.codec != nil &&
				m.rtpSession != nil &&
				m.srtpSession != nil &&
				m.relay.HasConnection()
			if ready {
				m.lastCaptureAt = time.Now()
				if opus, err := m.codec.Encode(frame); err == nil {
					m.sendOpusFrameLocked(opus)
				}
			}
			m.mu.Unlock()
		}
	}
}

// decodeMOHViaffmpeg uses ffmpeg to download and decode an MP3/audio URL to
// raw 16 kHz mono float32 PCM. Returns error if ffmpeg is unavailable.
func decodeMOHViaffmpeg(url string, sampleRate int) ([]float32, error) {
	cmd := exec.Command("ffmpeg",
		"-i", url,
		"-f", "s16le",
		"-ar", strconv.Itoa(sampleRate),
		"-ac", "1",
		"-loglevel", "quiet",
		"pipe:1",
	)
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	samples := len(raw) / 2
	pcm := make([]float32, samples)
	for i := range pcm {
		s := int16(binary.LittleEndian.Uint16(raw[i*2:]))
		pcm[i] = float32(s) / 32768.0
	}
	return pcm, nil
}
