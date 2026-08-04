package media

import "wacalls/internal/voip/core"
import "wacalls/internal/voip/media/nativemlow"

// codecAdapter wraps a core.AudioCodec to satisfy media.Codec.
type codecAdapter struct {
	inner core.AudioCodec
}

func (a *codecAdapter) Encode(pcm []float32) ([]byte, error) { return a.inner.Encode(pcm) }
func (a *codecAdapter) Decode(frame []byte) ([]float32, error) { return a.inner.Decode(frame) }
func (a *codecAdapter) FrameSize() int  { return a.inner.FrameSize() }
func (a *codecAdapter) SampleRate() int { return a.inner.SampleRate() }
func (a *codecAdapter) Close()          { a.inner.Close() }

// NewCodecWithNative creates the best available audio codec: the pure-Go MLow
// codec as baseline, then optionally wraps its encode path with the native CGO
// SMPL encoder if the "nativemlow" build tag is set and libopus is available.
// Returns the codec and a label: "native" or "go".
func NewCodecWithNative(opts CodecOptions) (Codec, string, error) {
	inner, err := NewMLowCodec(opts)
	if err != nil {
		return nil, "go", err
	}
	wrapped, mode := nativemlow.WrapEncoder(inner)
	return &codecAdapter{inner: wrapped}, mode, nil
}
