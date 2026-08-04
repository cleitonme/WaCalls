package media

import (
	"sync"
)

type RTCPReceiverStats struct {
	mu         sync.Mutex
	clockRate  uint32
	baseSeq    uint32
	maxSeq     uint16
	cycles     uint32
	received   uint32
	initSeq    bool
	expectPrev uint32
	recvPrev   uint32

	jitter    float64
	lastTs    uint32
	lastArriv uint32
	haveTs    bool

	lsr         uint32
	lsrArrival  uint64

	rttMs            float64
	hasRtt           bool
	rttSamples       uint32
	peerFractionLost uint8
}

func NewRTCPReceiverStats(clockRate uint32) *RTCPReceiverStats {
	return &RTCPReceiverStats{clockRate: clockRate}
}

func (r *RTCPReceiverStats) NoteRTP(seq uint16, rtpTs uint32, arrivalMs uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.initSeq {
		r.baseSeq = uint32(seq)
		r.maxSeq = seq
		r.initSeq = true
	} else if seq < r.maxSeq && uint16(r.maxSeq-seq) < 0x8000 {
	} else {
		if seq < r.maxSeq {
			r.cycles += 0x10000
		}
		r.maxSeq = seq
	}
	r.received++

	arriv := uint32(arrivalMs * uint64(r.clockRate) / 1000)
	if r.haveTs {
		transit := arriv - r.lastArriv
		tsDelta := rtpTs - r.lastTs
		d := int64(transit) - int64(tsDelta)
		if d < 0 {
			d = -d
		}
		r.jitter += (float64(d) - r.jitter) / 16.0
	}
	r.lastTs = rtpTs
	r.lastArriv = arriv
	r.haveTs = true
}

func (r *RTCPReceiverStats) NoteSenderReport(ntpMid uint32, arrivalMs uint64) {
	r.mu.Lock()
	r.lsr = ntpMid
	r.lsrArrival = arrivalMs
	r.mu.Unlock()
}

func (r *RTCPReceiverStats) NotePeerReportBlock(lsr, dlsr uint32, fractionLost uint8, nowMs uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peerFractionLost = fractionLost
	if lsr == 0 {
		return
	}
	a := mid32(nowMs)
	if a < lsr || a-lsr < dlsr {
		return
	}
	rtt := a - lsr - dlsr
	r.rttMs = float64(rtt) * 1000.0 / 65536.0
	r.hasRtt = true
	r.rttSamples++
}

func (r *RTCPReceiverStats) ReportBlock(peerSsrc uint32, nowMs uint64) RTCPReportBlock {
	r.mu.Lock()
	defer r.mu.Unlock()

	extHigh := r.cycles | uint32(r.maxSeq)
	expected := uint32(0)
	if r.initSeq {
		expected = extHigh - r.baseSeq + 1
	}
	cumulativeLost := expected - r.received

	expectedInterval := expected - r.expectPrev
	receivedInterval := r.received - r.recvPrev
	r.expectPrev = expected
	r.recvPrev = r.received
	lostInterval := int64(expectedInterval) - int64(receivedInterval)
	var fraction uint8
	if expectedInterval > 0 && lostInterval > 0 {
		fraction = uint8((lostInterval << 8) / int64(expectedInterval))
	}

	var dlsr uint32
	if r.lsr != 0 && nowMs >= r.lsrArrival {
		dlsr = uint32((nowMs - r.lsrArrival) * 65536 / 1000)
	}

	return RTCPReportBlock{
		SSRC:            peerSsrc,
		FractionLost:    fraction,
		CumulativeLost:  cumulativeLost & 0xffffff,
		ExtHighSeq:      extHigh,
		Jitter:          uint32(r.jitter),
		LSR:             r.lsr,
		DLSR:            dlsr,
	}
}

type CallQuality struct {
	RttMs        float64
	JitterMs     float64
	LossFraction float64
	HasRtt       bool
}

func (r *RTCPReceiverStats) QualitySnapshot(nowMs uint64) CallQuality {
	r.mu.Lock()
	defer r.mu.Unlock()
	var loss float64
	if r.initSeq {
		expected := (r.cycles | uint32(r.maxSeq)) - r.baseSeq + 1
		if expected > 0 {
			if cum := int64(expected) - int64(r.received); cum > 0 {
				loss = float64(cum) / float64(expected)
			}
		}
	}
	return CallQuality{
		RttMs:        r.rttMs,
		JitterMs:     r.jitter / float64(r.clockRate) * 1000.0,
		LossFraction: loss,
		HasRtt:       r.hasRtt,
	}
}

func mid32(nowMs uint64) uint32 {
	ntpSec := uint32(nowMs/1000 + ntpUnixOffsetSecs)
	ntpFrac := uint32(float64(nowMs%1000) / 1000.0 * 4294967296.0)
	return ntpSec<<16 | ntpFrac>>16
}
