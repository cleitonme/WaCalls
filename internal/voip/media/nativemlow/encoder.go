//go:build nativemlow

package nativemlow

/*
#cgo LDFLAGS: -lopus -lm
#include <opus.h>

static int enc_ctl_int(OpusEncoder *st, int request, opus_int32 value) {
	return opus_encoder_ctl(st, request, value);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	sampleRate   = 16000
	frameSamples = 960
	maxPacket    = 4000

	bitrate    = 20000
	complexity = 8
)

var (
	globalOnce   sync.Once
	liveEncoders atomic.Int64
)

type Encoder struct {
	mu      sync.Mutex
	st      *C.OpusEncoder
	cleanup runtime.Cleanup
}

func NewEncoder() (*Encoder, error) {
	globalOnce.Do(func() { C.opus_global_create() })
	var cerr C.int
	st := C.opus_encoder_create(sampleRate, 1, C.OPUS_APPLICATION_VOIP, &cerr)
	if cerr != C.OPUS_OK {
		return nil, fmt.Errorf("nativemlow: opus_encoder_create: %d", int(cerr))
	}
	for _, ctl := range [...]struct {
		req C.int
		val C.opus_int32
	}{
		{C.OPUS_SET_BITRATE_REQUEST, bitrate},
		{C.OPUS_SET_COMPLEXITY_REQUEST, complexity},
		{C.OPUS_SET_USE_SMPL_REQUEST, 1},
		{C.OPUS_SET_DTX_REQUEST, 0},
	} {
		if rc := C.enc_ctl_int(st, ctl.req, ctl.val); rc != C.OPUS_OK {
			C.opus_encoder_destroy(st)
			return nil, fmt.Errorf("nativemlow: encoder ctl %d: %d", int(ctl.req), int(rc))
		}
	}
	e := &Encoder{st: st}
	liveEncoders.Add(1)
	e.cleanup = runtime.AddCleanup(e, func(st *C.OpusEncoder) {
		C.opus_encoder_destroy(st)
		liveEncoders.Add(-1)
	}, st)
	return e, nil
}

func (e *Encoder) Encode(pcm []float32) ([]byte, error) {
	if len(pcm) != frameSamples {
		return nil, errors.New("nativemlow: expected 960 samples (60 ms @16 kHz)")
	}
	clean := make([]float32, frameSamples)
	for i, s := range pcm {
		switch {
		case math.IsNaN(float64(s)):
			s = 0.0
		case s < -1.0:
			s = -1.0
		case s > 1.0:
			s = 1.0
		}
		clean[i] = s
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st == nil {
		return nil, errors.New("nativemlow: encoder closed")
	}
	var buf [maxPacket]byte
	n := C.opus_encode_float(e.st,
		(*C.float)(unsafe.Pointer(&clean[0])), frameSamples,
		(*C.uchar)(unsafe.Pointer(&buf[0])), maxPacket)
	runtime.KeepAlive(e)
	if n < 0 {
		return nil, fmt.Errorf("nativemlow: opus_encode_float: %d", int(n))
	}
	out := make([]byte, int(n))
	copy(out, buf[:n])
	return out, nil
}

func (e *Encoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st == nil {
		return
	}
	e.cleanup.Stop()
	C.opus_encoder_destroy(e.st)
	runtime.KeepAlive(e)
	e.st = nil
	liveEncoders.Add(-1)
}

func LiveEncoders() int64 {
	return liveEncoders.Load()
}
