package call

import (
	"time"
	"wacalls/internal/voip/core"
	"wacalls/internal/voip/media"
	"wacalls/internal/voip/transport"
)

func (m *CallManager) initCodec() {
	if m.codec != nil {
		return
	}
	codec, mode, err := media.NewCodecWithNative(media.DefaultCodecOptions)
	if err != nil {
		m.log.Warn("MLow codec unavailable — call will run signaling-only (no audio)", "err", err)
		return
	}
	m.codec = codec
	m.jitter = media.NewJitterBuffer(3)
	m.recvStats = media.NewRTCPReceiverStats(16000)
	m.rtcpCName = "wacalls@" + m.ownCredJid()
	m.log.Info("codec initialized", "mode", mode)
}

func (m *CallManager) FeedCapturedPCM(data []float32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.onHold || m.codec == nil || len(data) == 0 {
		return
	}
	m.lastCaptureAt = time.Now()
	m.captureBuf = append(m.captureBuf, data...)
	if maxBuf := m.codec.FrameSize() * 4; len(m.captureBuf) > maxBuf {
		m.captureBuf = m.captureBuf[len(m.captureBuf)-maxBuf:]
	}
}

func (m *CallManager) startSendLoopLocked() {
	if m.sendLoopStop != nil || m.codec == nil {
		return
	}
	stop := make(chan struct{})
	m.sendLoopStop = stop
	frameSize := m.codec.FrameSize()
	go func() {
		ticker := time.NewTicker(60 * time.Millisecond)
		defer ticker.Stop()
		silence := make([]float32, frameSize)
		voiced := make([]float32, frameSize)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			m.mu.Lock()
			if m.codec == nil || m.rtpSession == nil || m.srtpSession == nil || !m.relay.HasConnection() {
				m.mu.Unlock()
				continue
			}
			frame := silence
			if len(m.captureBuf) >= frameSize {
				copy(voiced, m.captureBuf[:frameSize])
				frame = voiced
				m.captureBuf = m.captureBuf[frameSize:]
			}
			codec := m.codec
			onHold := m.onHold
			m.mu.Unlock()

			if onHold {
				continue
			}
			opus, err := codec.Encode(frame)
			if err != nil {
				m.log.Debug("encode error", "err", err)
				continue
			}
			m.mu.Lock()
			m.sendOpusFrameLocked(opus)
			m.mu.Unlock()
		}
	}()
}

func (m *CallManager) sendOpusFrameLocked(opus []byte) {
	if m.rtpSession == nil || m.srtpSession == nil {
		return
	}
	marker := !m.firstPacketSent
	pkt := m.rtpSession.CreatePacketWithDuration(opus, m.codec.FrameSize(), marker)
	if m.debeEnabled {
		pkt.Header.Extension = true
		pkt.Header.ExtensionProfile = 0xbede
		pkt.Header.ExtensionData = nil
	}
	m.firstPacketSent = true
	m.rtpPacketsSent++
	m.rtpOctetsSent += uint32(len(pkt.Payload))
	m.lastRtpTs = pkt.Header.Timestamp

	srtp, err := m.srtpSession.Protect(pkt)
	if err != nil {
		m.log.Debug("srtp protect error", "err", err)
		return
	}
	m.relay.Broadcast(srtp)
}

func (m *CallManager) startSilenceKeepaliveLocked() {
	if m.keepaliveStop != nil || m.codec == nil {
		return
	}
	stop := make(chan struct{})
	m.keepaliveStop = stop
	frameSize := m.codec.FrameSize()
	go func() {
		ticker := time.NewTicker(60 * time.Millisecond)
		defer ticker.Stop()
		silence := make([]float32, frameSize)
		silenceFrame, _ := m.codec.Encode(silence)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.mu.Lock()
				ready := m.codec != nil && m.rtpSession != nil && m.srtpSession != nil && m.relay.HasConnection()
				idle := time.Since(m.lastCaptureAt) > 120*time.Millisecond
				if ready && idle && silenceFrame != nil {
					m.sendOpusFrameLocked(silenceFrame)
				}
				m.mu.Unlock()
			}
		}
	}()
}

func (m *CallManager) onRelayData(data []byte) {
	if transport.IsStunPacket(data) {
		return
	}
	if transport.IsRtcpPacket(data) {
		m.handleRtcp(data)
		return
	}
	if !transport.IsRtpPacket(data) {
		return
	}
	if len(data) < 12 {
		return
	}
	pt := data[1] & 0x7f
	if pt != core.PayloadTypeWhatsAppOpus {
		return
	}

	m.mu.Lock()
	if m.srtpSession == nil || m.codec == nil {
		m.mu.Unlock()
		return
	}
	ssrc := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])
	if ssrc == m.selfSsrc {
		m.mu.Unlock()
		return
	}
	if !m.actualPeerSet {
		m.actualPeerSet = true
		if !containsSsrc(m.peerSsrcs, ssrc) {
			m.peerSsrcs = []uint32{ssrc}
			m.relay.SetSubscriptionSsrc(ssrc)
			go m.relay.ResendSubscriptions()
		}
	}
	srtp := m.srtpSession
	codec := m.codec
	jitter := m.jitter
	recvStats := m.recvStats
	selfSsrc := m.selfSsrc
	m.mu.Unlock()

	pkt, err := srtp.Unprotect(data)
	if err != nil {
		m.log.Debug("srtp unprotect error", "err", err)
		return
	}
	if len(pkt.Payload) == 0 {
		return
	}

	if recvStats != nil {
		recvStats.NoteRTP(pkt.Header.SequenceNumber, pkt.Header.Timestamp, uint64(time.Now().UnixMilli()))
	}

	m.mu.Lock()
	m.lastMediaRecv = time.Now()
	m.mu.Unlock()

	frames := jitter.Push(pkt.Header.SequenceNumber, pkt.Payload)

	m.mu.Lock()
	cb := m.OnPeerAudio
	var out [][]float32
	for _, fr := range frames {
		if fr.Present {
			pcm, err := codec.Decode(fr.Payload)
			if err != nil || len(pcm) == 0 {
				m.log.Debug("decode error", "err", err, "payload_len", len(fr.Payload))
				continue
			}
			m.lastFrame = pcm
			m.concealed = 0
			out = append(out, media.NormalizeFrame(pcm, codec.FrameSize()))
		} else {
			concealed := m.concealLocked()
			if concealed != nil {
				out = append(out, concealed)
			}
		}
	}
	_ = selfSsrc
	m.mu.Unlock()

	if cb != nil {
		for _, pcm := range out {
			cb(pcm)
		}
	}
}

func (m *CallManager) concealLocked() []float32 {
	n := m.codec.FrameSize()
	if len(m.lastFrame) > 0 {
		n = len(m.lastFrame)
	}
	pcm := make([]float32, n)
	if m.concealed == 0 && len(m.lastFrame) > 1 {
		last := len(pcm) - 1
		for i := range pcm {
			pcm[i] = m.lastFrame[i] * (1 - float32(i)/float32(last))
		}
	}
	m.concealed++
	return pcm
}
