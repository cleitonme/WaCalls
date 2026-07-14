package main

import (
	"encoding/json"
	"net/http"

	"wacalls/internal/voip/core"
)

// defaultPickupMOHURL plays while a call is on hold during a pickup/transfer
// handoff, if the caller doesn't supply its own moh_url.
const defaultPickupMOHURL = "https://scripts.infomeurer.com.br/toque.mp3"

// doPickup transfers an active call to a different agent within the same session.
// The WhatsApp session (sid) is shared by all agents — the call never changes session.
//
// The call is put on hold (MOH injected server-side, independent of any
// browser) for the duration of the handoff. This decouples the call's
// survival from agent A's browser WebRTC connection — previously, if agent
// A's bridge ICE died (network hiccup, NAT timeout, etc.) at any point
// during the — often slow — pickup dialog flow, the call would be torn down
// for real before agent B's negotiation even started. Holding means WhatsApp
// keeps hearing MOH audio no matter what agent A's browser does; the call
// only resumes real two-way audio once agent B's bridge is confirmed (see
// doWebRTC's auto-unhold).
//
// Flow:
//  1. Agent B POSTs /api/sessions/{sid}/calls/{id}/pickup
//  2. Call is put on hold with MOH; emits call-held.
//  3. Emits call-picked-up so agent A's frontend knows to stop showing itself as active.
//  4. Returns 200 with sessionId — agent B then POSTs /api/sessions/{sid}/calls/{id}/webrtc,
//     which atomically swaps in the new bridge, closes the old one, and auto-unholds.
func (s *server) doPickup(sess *Session, w http.ResponseWriter, r *http.Request) {
	callID := r.PathValue("id")

	ac, ok := sess.reg.get(callID)
	if !ok {
		// Try broker to distinguish "call ended" from "wrong session".
		if rec, inBroker := s.broker.getCall(callID); !inBroker {
			s.log.Info("pickup 404: call not in registry nor broker (already ended)", "session", sess.id, "call_id", callID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "call not found or already ended"})
		} else {
			s.log.Info("pickup 404: call in broker but not this session's registry", "session", sess.id, "call_id", callID, "broker_session", rec.SessionID, "broker_status", string(rec.Status))
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "call not in session registry (broker has it — session mismatch?)"})
		}
		return
	}

	ci := ac.cm.CurrentCall()
	if ci == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active call"})
		return
	}

	var body struct {
		MOHURL string `json:"moh_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	mohURL := body.MOHURL
	if mohURL == "" {
		mohURL = defaultPickupMOHURL
	}

	if ci.StateData.State == core.CallStateActive {
		if err := ac.cm.HoldCall(mohURL); err != nil {
			s.log.Warn("pickup: hold call failed, proceeding without MOH", "session", sess.id, "call_id", callID, "err", err)
		} else {
			// The call no longer depends on agent A's bridge while held — don't
			// let a flaky ICE connection on their end kill the real call mid-handoff.
			sess.reg.disableTerminalICE(callID)
			s.log.Info("pickup: call on hold with MOH for handoff", "session", sess.id, "call_id", callID, "moh_url", mohURL)
			s.broker.emitCallHeld(sess.id, callID)
		}
	}

	s.log.Info("pickup ok — emitting call-picked-up", "session", sess.id, "call_id", callID)
	s.broker.emitCallPickedUp(sess.id, callID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        callID,
		"sessionId": sess.id,
		"state":     string(ci.StateData.State),
	})
}
