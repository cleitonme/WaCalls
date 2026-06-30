package main

import (
	"encoding/json"
	"net/http"

	"wacalls/internal/voip/core"
)

// doPickup moves a held call from source_sid into the target session (sid).
// Flow:
//  1. Caller (agent B) POSTs to /api/sessions/{sid}/calls/{id}/pickup with body {"source_sid":"..."}.
//  2. Server locates the held call in source session.
//  3. Removes it from source registry (no hang-up), closes source WebRTC bridge.
//  4. Adds it to target registry and rewires CallManager callbacks.
//  5. Unholds the call (stops MOH).
//  6. Target agent then POSTs /api/sessions/{sid}/calls/{id}/webrtc to open their audio channel.
func (s *server) doPickup(targetSess *Session, w http.ResponseWriter, r *http.Request) {
	callID := r.PathValue("id")

	var body struct {
		SourceSID string `json:"source_sid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SourceSID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source_sid required"})
		return
	}
	if body.SourceSID == targetSess.id {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source_sid must differ from target session"})
		return
	}

	sourceSess, ok := s.sessions.Get(body.SourceSID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "source session not found"})
		return
	}

	// Peek first (check state), then atomically remove.
	peekAC, ok := sourceSess.reg.get(callID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "call not found in source session"})
		return
	}
	ci := peekAC.cm.CurrentCall()
	if ci == nil || ci.StateData.State != core.CallStateOnHold {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "call is not on hold in source session"})
		return
	}

	// Remove from source without ending the WhatsApp call.
	ac, removed := sourceSess.reg.remove(callID)
	if !removed {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "call already removed from source session"})
		return
	}

	// Close source WebRTC bridge so audio stops flowing to source agent.
	if ac.bridge != nil {
		ac.bridge.Close()
		ac.bridge = nil
	}

	// Register under target session.
	targetSess.reg.add(callID, ac)

	// Rewire CallManager event callbacks so future state changes route to target.
	targetSess.wireCall(ac.cm, callID)

	// Unhold — stops MOH and resumes normal media path for the target.
	if err := ac.cm.UnholdCall(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "unhold failed: " + err.Error()})
		return
	}

	// Update broker so the call record reflects the new session.
	if rec, ok := s.broker.getCall(callID); ok {
		rec.SessionID = targetSess.id
		s.broker.upsertCall(*rec)
	}

	s.broker.emitCallUnheld(targetSess.id, callID)
	s.broker.emitCallPickedUp(targetSess.id, callID, body.SourceSID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        callID,
		"state":     "active",
		"sessionId": targetSess.id,
	})
}
