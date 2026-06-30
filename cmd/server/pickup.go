package main

import (
	"net/http"
)

// doPickup transfers an active call to a different agent within the same session.
// The WhatsApp session (sid) is shared by all agents — the call never changes session.
// Pickup closes the current WebRTC bridge so a new agent can connect via /webrtc.
//
// Flow:
//  1. Agent B POSTs /api/sessions/{sid}/calls/{id}/pickup
//  2. Server closes the existing WebRTC bridge (disconnects agent A's audio).
//  3. Emits call-picked-up so agent A's frontend knows it lost the call.
//  4. Returns 200 with sessionId — agent B then POSTs /api/sessions/{sid}/calls/{id}/webrtc.
func (s *server) doPickup(sess *Session, w http.ResponseWriter, r *http.Request) {
	callID := r.PathValue("id")

	ac, ok := sess.reg.get(callID)
	if !ok {
		// Try broker to distinguish "call ended" from "wrong session".
		if _, inBroker := s.broker.getCall(callID); !inBroker {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "call not found or already ended"})
		} else {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "call not in session registry (broker has it — session mismatch?)"})
		}
		return
	}

	ci := ac.cm.CurrentCall()
	if ci == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active call"})
		return
	}

	// Disconnect current agent's audio bridge; new agent attaches via /webrtc.
	// Use closeBridge (not bridge.Close directly) to avoid OnTerminalICE firing
	// and dropping the WhatsApp call.
	if ac.bridge != nil {
		old, _ := sess.reg.setBridge(callID, nil)
		if old != nil {
			closeBridge(old)
		}
	}

	s.broker.emitCallPickedUp(sess.id, callID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        callID,
		"sessionId": sess.id,
		"state":     string(ci.StateData.State),
	})
}
