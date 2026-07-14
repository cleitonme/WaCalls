package main

import (
	"net/http"
)

// doPickup transfers an active call to a different agent within the same session.
// The WhatsApp session (sid) is shared by all agents — the call never changes session.
//
// The old agent's WebRTC bridge is intentionally left running here. Closing it
// up front (as done previously) left the call with no audio bridge at all for
// the entire duration of agent B's getUserMedia + ICE negotiation, which is
// what caused most pickups to drop the call (WA-side media inactivity).
// Instead, agent A's bridge keeps feeding audio until agent B calls /webrtc,
// at which point sess.setBridge swaps bridges atomically (old one is closed
// only once the new one is already in place — near-zero gap). If agent B's
// negotiation fails, agent A's call simply keeps working.
//
// Flow:
//  1. Agent B POSTs /api/sessions/{sid}/calls/{id}/pickup
//  2. Emits call-picked-up so agent A's frontend knows to stop showing itself as active.
//  3. Returns 200 with sessionId — agent B then POSTs /api/sessions/{sid}/calls/{id}/webrtc,
//     which atomically swaps in the new bridge and closes the old one.
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

	s.broker.emitCallPickedUp(sess.id, callID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        callID,
		"sessionId": sess.id,
		"state":     string(ci.StateData.State),
	})
}
