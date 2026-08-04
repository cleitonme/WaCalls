package media

import (
	"encoding/binary"
)

const (
	RTCPPayloadTypeSR       = 200
	RTCPPayloadTypeRR       = 201
	RTCPPayloadTypeSDES     = 202
	RTCPPayloadTypeCompact  = 208
	RTCPPayloadTypeCompact2 = 209
)

const ntpUnixOffsetSecs = 2208988800

const sdesItemCNAME = 1

type RTCPSenderStats struct {
	PacketsSent  uint32
	OctetsSent   uint32
	RtpTimestamp uint32
}

type RTCPReportBlock struct {
	SSRC            uint32
	FractionLost    uint8
	CumulativeLost   uint32
	ExtHighSeq       uint32
	Jitter           uint32
	LSR              uint32
	DLSR             uint32
}

type InboundRTCP struct {
	HasSR    bool
	SRNtpMid uint32
	Blocks   []RTCPReportBlock
}

func BuildCompact208(local, remote uint32) [12]byte {
	var buf [12]byte
	buf[0] = 0x81
	buf[1] = RTCPPayloadTypeCompact
	buf[3] = 2
	binary.BigEndian.PutUint32(buf[4:8], local)
	binary.BigEndian.PutUint32(buf[8:12], remote)
	return buf
}

func BuildCompact209(local uint32) [8]byte {
	var buf [8]byte
	buf[0] = 0x81
	buf[1] = RTCPPayloadTypeCompact2
	buf[3] = 1
	binary.BigEndian.PutUint32(buf[4:8], local)
	return buf
}

func BuildSenderReportWithBlock(local uint32, s RTCPSenderStats, rb *RTCPReportBlock, nowMs uint64) []byte {
	size := 28
	rc := byte(0)
	if rb != nil {
		size = 52
		rc = 1
	}
	buf := make([]byte, size)
	buf[0] = 0x80 | rc
	buf[1] = RTCPPayloadTypeSR
	binary.BigEndian.PutUint16(buf[2:4], uint16(size/4-1))
	binary.BigEndian.PutUint32(buf[4:8], local)
	ntpSec := uint32(nowMs/1000 + ntpUnixOffsetSecs)
	ntpFrac := uint32(float64(nowMs%1000) / 1000.0 * 4294967296.0)
	binary.BigEndian.PutUint32(buf[8:12], ntpSec)
	binary.BigEndian.PutUint32(buf[12:16], ntpFrac)
	binary.BigEndian.PutUint32(buf[16:20], s.RtpTimestamp)
	binary.BigEndian.PutUint32(buf[20:24], s.PacketsSent)
	binary.BigEndian.PutUint32(buf[24:28], s.OctetsSent)
	if rb != nil {
		binary.BigEndian.PutUint32(buf[28:32], rb.SSRC)
		buf[32] = rb.FractionLost
		buf[33] = byte(rb.CumulativeLost >> 16)
		buf[34] = byte(rb.CumulativeLost >> 8)
		buf[35] = byte(rb.CumulativeLost)
		binary.BigEndian.PutUint32(buf[36:40], rb.ExtHighSeq)
		binary.BigEndian.PutUint32(buf[40:44], rb.Jitter)
		binary.BigEndian.PutUint32(buf[44:48], rb.LSR)
		binary.BigEndian.PutUint32(buf[48:52], rb.DLSR)
	}
	return buf
}

func BuildSDES(ssrc uint32, cname string) []byte {
	chunk := make([]byte, 0, 4+2+len(cname)+1)
	var ssrcBuf [4]byte
	binary.BigEndian.PutUint32(ssrcBuf[:], ssrc)
	chunk = append(chunk, ssrcBuf[:]...)
	chunk = append(chunk, sdesItemCNAME, byte(len(cname)))
	chunk = append(chunk, cname...)
	chunk = append(chunk, 0x00)
	if pad := (4 - (4+len(chunk))%4) % 4; pad > 0 {
		chunk = append(chunk, make([]byte, pad)...)
	}
	buf := make([]byte, 4+len(chunk))
	buf[0] = 0x81
	buf[1] = RTCPPayloadTypeSDES
	binary.BigEndian.PutUint16(buf[2:4], uint16((4+len(chunk))/4-1))
	copy(buf[4:], chunk)
	return buf
}

func BuildRTCPCompound(local uint32, s RTCPSenderStats, rb *RTCPReportBlock, cname string, nowMs uint64) []byte {
	return append(BuildSenderReportWithBlock(local, s, rb, nowMs),
		BuildSDES(local, cname)...)
}

func ParseRTCPCompound(data []byte) InboundRTCP {
	var out InboundRTCP
	for off := 0; off+4 <= len(data); {
		if (data[off]>>6)&0x03 != 2 {
			break
		}
		rc := int(data[off] & 0x1f)
		pt := data[off+1]
		pktLen := (int(binary.BigEndian.Uint16(data[off+2:off+4])) + 1) * 4
		if pktLen < 4 || off+pktLen > len(data) {
			break
		}
		switch pt {
		case RTCPPayloadTypeSR:
			if pktLen >= 28 {
				out.HasSR = true
				out.SRNtpMid = binary.BigEndian.Uint32(data[off+10 : off+14])
				parseReportBlocks(data[off+28:off+pktLen], rc, &out)
			}
		case RTCPPayloadTypeRR:
			if pktLen >= 8 {
				parseReportBlocks(data[off+8:off+pktLen], rc, &out)
			}
		}
		off += pktLen
	}
	return out
}

func ParseRTCPSenderSSRC(data []byte) (uint32, bool) {
	if len(data) < 8 {
		return 0, false
	}
	if (data[0]>>6)&0x03 != 2 {
		return 0, false
	}
	pt := data[1]
	if pt != RTCPPayloadTypeSR && pt != RTCPPayloadTypeRR {
		return 0, false
	}
	return binary.BigEndian.Uint32(data[4:8]), true
}

func parseReportBlocks(data []byte, count int, out *InboundRTCP) {
	off := 0
	for i := 0; i < count && off+24 <= len(data); i++ {
		rb := RTCPReportBlock{
			SSRC:          binary.BigEndian.Uint32(data[off : off+4]),
			FractionLost:  data[off+4],
			CumulativeLost: uint32(data[off+5])<<16 | uint32(data[off+6])<<8 | uint32(data[off+7]),
			ExtHighSeq:    binary.BigEndian.Uint32(data[off+8 : off+12]),
			Jitter:        binary.BigEndian.Uint32(data[off+12 : off+16]),
			LSR:           binary.BigEndian.Uint32(data[off+16 : off+20]),
			DLSR:          binary.BigEndian.Uint32(data[off+20 : off+24]),
		}
		out.Blocks = append(out.Blocks, rb)
		off += 24
	}
}
