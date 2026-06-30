package mlow

import (
	"math"
	"sync"
)

// cpx is a single-precision complex value.
//
// Source of truth: https://github.com/oxidezap/whatsapp-rust/blob/674e85164b35ca19115dfebcf605708d15951ee7/wacore/src/voip/mlow/smpl_perc.rs#L318-L343
type cpx struct {
	re, im float32
}

func (a cpx) add(b cpx) cpx {
	return cpx{re: a.re + b.re, im: a.im + b.im}
}

func (a cpx) mul(b cpx) cpx {
	return cpx{
		re: a.re*b.re - a.im*b.im,
		im: a.re*b.im + a.im*b.re,
	}
}

// smallestFactor returns the smallest prime factor of n (>= 2).
func smallestFactor(n int) int {
	// Source of truth: https://github.com/oxidezap/whatsapp-rust/blob/674e85164b35ca19115dfebcf605708d15951ee7/wacore/src/voip/mlow/smpl_perc.rs#L346-L358
	if n%2 == 0 {
		return 2
	}
	p := 3
	for p*p <= n {
		if n%p == 0 {
			return p
		}
		p += 2
	}
	return n
}

// fftTwEntry holds pre-computed forward (sign=-1) twiddle factors for one FFT sub-problem size n.
//
//   - non-prime n: tws[k*p + q] = exp(-2πi·k·q/n), len = n*p
//   - prime n:     tws[k*n + j] = exp(-2πi·k·j/n), len = n*n
type fftTwEntry struct {
	p   int
	tws []cpx
}

var (
	fftTwMu  sync.RWMutex
	fftTwMap = make(map[int]fftTwEntry, 32)
)

// fftGetTwiddles returns (and lazily builds) the twiddle table for size n.
// Hot path: RLock only. Cold path (first call per size): exclusive lock.
func fftGetTwiddles(n int) fftTwEntry {
	fftTwMu.RLock()
	e, ok := fftTwMap[n]
	fftTwMu.RUnlock()
	if ok {
		return e
	}
	// Cold path — build and store.
	fftTwMu.Lock()
	// Double-check after acquiring write lock.
	if e, ok = fftTwMap[n]; ok {
		fftTwMu.Unlock()
		return e
	}
	p := smallestFactor(n)
	var tws []cpx
	if p == n { // prime DFT
		tws = make([]cpx, n*n)
		for k := 0; k < n; k++ {
			for j := 0; j < n; j++ {
				ang := -2.0 * math.Pi * float64(k*j) / float64(n)
				tws[k*n+j] = cpx{float32(math.Cos(ang)), float32(math.Sin(ang))}
			}
		}
	} else {
		tws = make([]cpx, n*p)
		for k := 0; k < n; k++ {
			for q := 0; q < p; q++ {
				ang := -2.0 * math.Pi * float64(k*q) / float64(n)
				tws[k*p+q] = cpx{float32(math.Cos(ang)), float32(math.Sin(ang))}
			}
		}
	}
	e = fftTwEntry{p: p, tws: tws}
	fftTwMap[n] = e
	fftTwMu.Unlock()
	return e
}

// prewarmFftTwiddles recursively pre-builds twiddle tables for n and all its
// sub-problem sizes so the hot path never takes the write-lock.
func prewarmFftTwiddles(n int) {
	if n <= 1 {
		return
	}
	fftGetTwiddles(n)
	p := smallestFactor(n)
	prewarmFftTwiddles(n / p)
}

// fftWorkspace holds pre-allocated buffers for one cfft/rfft call of a fixed size.
// Embed in long-lived state (PercModelState, SmplEncoderState) to avoid per-frame allocs.
type fftWorkspace struct {
	n       int
	cin     []cpx
	spec    []cpx
	scratch []cpx
}

// newFftWorkspace allocates a workspace for FFTs of size n.
func newFftWorkspace(n int) fftWorkspace {
	return fftWorkspace{
		n:       n,
		cin:     make([]cpx, n),
		spec:    make([]cpx, n),
		scratch: make([]cpx, 2*n),
	}
}

// fftRecW is the twiddle-cached, scratch-buffered recursive DFT.
// sign=-1 for forward, +1 for inverse. scratch must have >= 2*n elements.
// Zero allocations on the hot path (all twiddles pre-warmed).
func fftRecW(x []cpx, stride, n int, sign float32, out, scratch []cpx) {
	if n == 1 {
		out[0] = x[0]
		return
	}
	tw := fftGetTwiddles(n)
	p := tw.p
	m := n / p

	if p == n { // prime DFT
		for k := 0; k < n; k++ {
			var acc cpx
			base := k * n
			for j := 0; j < n; j++ {
				tw2 := tw.tws[base+j]
				if sign > 0 {
					tw2.im = -tw2.im
				}
				acc = acc.add(x[j*stride].mul(tw2))
			}
			out[k] = acc
		}
		return
	}

	sub := scratch[:n]
	nextScratch := scratch[n:]
	for q := 0; q < p; q++ {
		fftRecW(x[q*stride:], stride*p, m, sign, sub[q*m:(q+1)*m], nextScratch)
	}
	for k := 0; k < n; k++ {
		kmod := k % m
		var acc cpx
		base := k * p
		for q := 0; q < p; q++ {
			tw2 := tw.tws[base+q]
			if sign > 0 {
				tw2.im = -tw2.im
			}
			acc = acc.add(sub[q*m+kmod].mul(tw2))
		}
		out[k] = acc
	}
}

// cfftW computes the complex FFT using pre-allocated workspace (no allocs on warm path).
func cfftW(input, out []cpx, sign float32, ws *fftWorkspace) {
	fftRecW(input, 1, ws.n, sign, out, ws.scratch)
}

// fftRec is the legacy path (allocates sub-scratch). Still uses twiddle cache.
func fftRec(x []cpx, stride, n int, sign float32, out []cpx) {
	if n == 1 {
		out[0] = x[0]
		return
	}
	tw := fftGetTwiddles(n)
	p := tw.p
	m := n / p

	if p == n {
		for k := 0; k < n; k++ {
			var acc cpx
			base := k * n
			for j := 0; j < n; j++ {
				tw2 := tw.tws[base+j]
				if sign > 0 {
					tw2.im = -tw2.im
				}
				acc = acc.add(x[j*stride].mul(tw2))
			}
			out[k] = acc
		}
		return
	}
	sub := make([]cpx, n)
	for q := 0; q < p; q++ {
		fftRec(x[q*stride:], stride*p, m, sign, sub[q*m:(q+1)*m])
	}
	for k := 0; k < n; k++ {
		kmod := k % m
		var acc cpx
		base := k * p
		for q := 0; q < p; q++ {
			tw2 := tw.tws[base+q]
			if sign > 0 {
				tw2.im = -tw2.im
			}
			acc = acc.add(sub[q*m+kmod].mul(tw2))
		}
		out[k] = acc
	}
}

// cfft computes the complex FFT (legacy allocating path, used by tests / rfftBackwardOrdered).
func cfft(input, out []cpx, sign float32) {
	// Source of truth: https://github.com/oxidezap/whatsapp-rust/blob/674e85164b35ca19115dfebcf605708d15951ee7/wacore/src/voip/mlow/smpl_perc.rs#L408-L412
	fftRec(input, 1, len(input), sign, out)
}

// rfftForwardOrdered is the forward real FFT (legacy allocating path).
func rfftForwardOrdered(time, f []float32) {
	// Source of truth: https://github.com/oxidezap/whatsapp-rust/blob/674e85164b35ca19115dfebcf605708d15951ee7/wacore/src/voip/mlow/smpl_perc.rs#L416-L432
	n := len(time)
	cin := make([]cpx, n)
	for i := 0; i < n; i++ {
		cin[i].re = time[i]
	}
	spec := make([]cpx, n)
	cfft(cin, spec, -1.0)
	f[0] = spec[0].re
	f[1] = spec[n/2].re
	for i := 1; i < n/2; i++ {
		f[2*i] = spec[i].re
		f[2*i+1] = spec[i].im
	}
}

// rfftForwardOrderedW is rfftForwardOrdered using pre-allocated workspace (no allocs).
func rfftForwardOrderedW(time []float32, f []float32, ws *fftWorkspace) {
	n := ws.n
	cin := ws.cin
	for i := 0; i < n; i++ {
		cin[i] = cpx{re: time[i]}
	}
	spec := ws.spec
	cfftW(cin, spec, -1.0, ws)
	f[0] = spec[0].re
	f[1] = spec[n/2].re
	for i := 1; i < n/2; i++ {
		f[2*i] = spec[i].re
		f[2*i+1] = spec[i].im
	}
}

// rfftBackwardOrderedW is the inverse real FFT using pre-allocated workspace (no allocs).
func rfftBackwardOrderedW(f []float32, time []float32, ws *fftWorkspace) {
	n := ws.n
	spec := ws.cin // reuse cin buffer as spec input
	spec[0] = cpx{f[0], 0}
	spec[n/2] = cpx{f[1], 0}
	for i := 1; i < n/2; i++ {
		re := f[2*i]
		im := f[2*i+1]
		spec[i] = cpx{re, im}
		spec[n-i] = cpx{re, -im}
	}
	tout := ws.spec
	cfftW(spec, tout, 1.0, ws)
	for i := 0; i < n; i++ {
		time[i] = tout[i].re
	}
}
