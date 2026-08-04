package call

import (
	"time"
	"wacalls/internal/voip/media"
)

const (
	rtcp208Interval = time.Second
	rtcpSRInterval  = 1500 * time.Millisecond
	rtcp209Interval = 3 * time.Second
)

func (m *CallManager) startRtcpTxLocked() {
	if m.rtcpTxStop != nil {
		return
	}
	stop := make(chan struct{})
	m.rtcpTxStop = stop
	go m.runRtcpTx(stop)
}

func (m *CallManager) runRtcpTx(stop chan struct{}) {
	t208 := time.NewTicker(rtcp208Interval)
	tSR := time.NewTicker(rtcpSRInterval)
	t209 := time.NewTicker(rtcp209Interval)
	defer t208.Stop()
	defer tSR.Stop()
	defer t209.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t208.C:
			m.emitRtcp208()
		case <-tSR.C:
			m.emitRtcpSR()
		case <-t209.C:
			m.emitRtcp209()
		}
	}
}

func (m *CallManager) emitRtcp208() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srtpSession == nil || m.rtpSession == nil || !m.relay.HasConnection() {
		return
	}
	peer := firstSsrc(m.peerSsrcs)
	pkt := media.BuildCompact208(m.selfSsrc, peer)
	m.relay.Broadcast(pkt[:])
}

func (m *CallManager) emitRtcp209() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srtpSession == nil || m.rtpSession == nil || !m.relay.HasConnection() {
		return
	}
	pkt := media.BuildCompact209(m.selfSsrc)
	m.relay.Broadcast(pkt[:])
}

func (m *CallManager) emitRtcpSR() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srtpSession == nil || m.rtpSession == nil || !m.relay.HasConnection() {
		return
	}
	stats := media.RTCPSenderStats{
		PacketsSent:  m.rtpPacketsSent,
		OctetsSent:   m.rtpOctetsSent,
		RtpTimestamp: m.lastRtpTs,
	}
	now := uint64(time.Now().UnixMilli())
	var rb *media.RTCPReportBlock
	if peer := firstSsrc(m.peerSsrcs); m.recvStats != nil && peer != 0 {
		b := m.recvStats.ReportBlock(peer, now)
		rb = &b
	}
	pkt := media.BuildRTCPCompound(m.selfSsrc, stats, rb, m.rtcpCName, now)
	m.relay.Broadcast(pkt)
}

func (m *CallManager) handleRtcp(data []byte) {
	m.mu.Lock()
	recvStats := m.recvStats
	selfSsrc := m.selfSsrc
	m.mu.Unlock()

	if recvStats == nil {
		return
	}

	now := uint64(time.Now().UnixMilli())
	in := media.ParseRTCPCompound(data)
	if in.HasSR {
		recvStats.NoteSenderReport(in.SRNtpMid, now)
	}
	for _, b := range in.Blocks {
		if b.SSRC == selfSsrc && b.LSR != 0 {
			recvStats.NotePeerReportBlock(b.LSR, b.DLSR, b.FractionLost, now)
		}
	}
}
