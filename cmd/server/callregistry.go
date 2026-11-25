package main

import (
	"sync"

	"wacalls/internal/voip/call"
)

// mediaBridge é o contrato comum das pontes de áudio do navegador: WebRTC
// (Bridge) e WebSocket (wsBridge). WritePCM leva o downlink (WhatsApp→navegador)
// como PCM float32 16 kHz mono; DisableTerminate evita que a queda da ponte
// encerre a chamada; Close libera a conexão.
type mediaBridge interface {
	WritePCM(pcm []float32) error
	DisableTerminate()
	Close()
}

type activeCall struct {
	cm          *call.CallManager
	bridge      mediaBridge
	stopRinging func()
}

type callRegistry struct {
	mu    sync.Mutex
	calls map[string]*activeCall
}

func newCallRegistry() *callRegistry {
	return &callRegistry{calls: map[string]*activeCall{}}
}

func (r *callRegistry) add(callID string, ac *activeCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[callID] = ac
}

func (r *callRegistry) get(callID string) (*activeCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ac, ok := r.calls[callID]
	return ac, ok
}

func (r *callRegistry) remove(callID string) (*activeCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ac, ok := r.calls[callID]
	if !ok {
		return nil, false
	}
	delete(r.calls, callID)
	return ac, true
}

func (r *callRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *callRegistry) setBridge(callID string, b mediaBridge) (mediaBridge, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ac, ok := r.calls[callID]
	if !ok {
		return nil, false
	}
	oldB := ac.bridge
	ac.bridge = b
	return oldB, true
}

// disableTerminalICE silences the current bridge's OnTerminalICE hook without
// closing it. Used when a call goes on hold for a pickup/transfer handoff:
// the call's survival no longer depends on this bridge, so a flaky ICE
// connection on the outgoing agent's side must not be allowed to end the
// real call while the handoff is in flight.
func (r *callRegistry) disableTerminalICE(callID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ac, ok := r.calls[callID]; ok && ac.bridge != nil {
		ac.bridge.DisableTerminate()
	}
}

func (r *callRegistry) drain() []*activeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*activeCall, 0, len(r.calls))
	for _, ac := range r.calls {
		out = append(out, ac)
	}
	r.calls = map[string]*activeCall{}
	return out
}
