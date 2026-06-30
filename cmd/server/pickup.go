package main

import (
	"net/http"
)

// doPickup hands an active call to a different agent within the same session.
// The WhatsApp session (sid) is fixed — it represents the phone number/connection.
// Multiple agents share one sid. Transfer = close agent A's WebRTC bridge so
// agent B can open theirs via POST /webrtc on the same callID.
//
// Flow:
//  1. Agent B POSTs /api/sessions/{sid}/calls/{id}/pickup
//  2. Server finds the call in the session registry (no session move).
//  3. Closes the existing WebRTC bridge (disconnects agent A's audio).
//  4. Emits call-picked-up so agent A's frontend knows it lost the call.
//  5. Returns 200 — agent B then POSTs /api/sessions/{sid}/calls/{id}/webrtc.
func (s *server) doPickup(sess *Session, w http.ResponseWriter, r *http.Request) {
	callID := r.PathValue("id")

	ac, ok := sess.reg.get(callID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "call not found in session"})
		return
	}
	if ac.cm.CurrentCall() == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active call"})
		return
	}

	// Disconnect current agent's audio bridge; the new agent will attach via /webrtc.
	if ac.bridge != nil {
		old, _ := sess.reg.setBridge(callID, nil)
		if old != nil {
			old.Close()
		}
	}

	s.broker.emitCallPickedUp(sess.id, callID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        callID,
		"sessionId": sess.id,
		"state":     string(ac.cm.CurrentCall().StateData.State),
	})
}
