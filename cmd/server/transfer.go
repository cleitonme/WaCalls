package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"wacalls/internal/voip/core"

	"go.mau.fi/whatsmeow/types"
)

func (s *server) doTransfer(sess *Session, w http.ResponseWriter, r *http.Request) {
	callID := r.PathValue("id")

	ac, ok := sess.reg.get(callID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such call"})
		return
	}
	ci := ac.cm.CurrentCall()
	if ci == nil || ci.StateData.State != core.CallStateActive {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "call is not in active state"})
		return
	}

	var body struct {
		TargetSID string `json:"target_sid"`
		MOHURL    string `json:"moh_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TargetSID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_sid required"})
		return
	}
	if body.TargetSID == sess.id {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_sid must differ from source session"})
		return
	}

	targetSess, ok := s.sessions.Get(body.TargetSID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target session not found"})
		return
	}
	if targetSess.client.Store.ID == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "target session not paired"})
		return
	}

	peerJID, err := types.ParseJID(ci.PeerJid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid peer JID: " + err.Error()})
		return
	}

	if err := ac.cm.HoldCall(body.MOHURL); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.broker.emitCallHeld(sess.id, callID)

	transferCallID, err := targetSess.startOutgoing(r.Context(), peerJID, false)
	if err != nil {
		_ = ac.cm.UnholdCall()
		s.broker.emitCallUnheld(sess.id, callID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.broker.emitTransferStarted(sess.id, callID, transferCallID)
	go s.monitorTransfer(sess, callID, targetSess, transferCallID)

	writeJSON(w, http.StatusOK, map[string]any{
		"original_call_id": callID,
		"transfer_call_id": transferCallID,
		"target_sid":       body.TargetSID,
		"state":            "transferring",
	})
}

func (s *server) monitorTransfer(
	origSess *Session, origCallID string,
	transferSess *Session, transferCallID string,
) {
	const (
		pollInterval   = 500 * time.Millisecond
		transferTimeout = 60 * time.Second
	)
	deadline := time.Now().Add(transferTimeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		<-ticker.C

		ac, ok := transferSess.reg.get(transferCallID)
		if !ok {
			// Removed from registry → ended/rejected before becoming active.
			s.unholdAndNotify(origSess, origCallID, transferCallID, "rejected")
			return
		}

		ci := ac.cm.CurrentCall()
		if ci == nil || ci.IsEnded() {
			s.unholdAndNotify(origSess, origCallID, transferCallID, "rejected")
			return
		}

		if ci.IsActive() {
			// Transfer succeeded — terminate the original held call.
			if origAC, ok := origSess.reg.get(origCallID); ok {
				_ = origAC.cm.EndCall(context.Background(), core.EndCallReasonUserEnded)
			}
			origSess.removeCall(origCallID)
			s.broker.endCall(origCallID, string(core.EndCallReasonUserEnded))
			s.broker.emitTransferCompleted(origSess.id, origCallID, transferCallID)
			return
		}

		if time.Now().After(deadline) {
			// Timeout — abort transfer, restore original call.
			if transferAC, ok := transferSess.reg.get(transferCallID); ok {
				_ = transferAC.cm.EndCall(context.Background(), core.EndCallReasonTimeout)
			}
			transferSess.removeCall(transferCallID)
			s.broker.endCall(transferCallID, string(core.EndCallReasonTimeout))
			s.unholdAndNotify(origSess, origCallID, transferCallID, "timeout")
			return
		}
	}
}

func (s *server) unholdAndNotify(origSess *Session, origCallID, transferCallID, reason string) {
	if ac, ok := origSess.reg.get(origCallID); ok {
		_ = ac.cm.UnholdCall()
		s.broker.emitCallUnheld(origSess.id, origCallID)
	}
	s.broker.emitTransferFailed(origSess.id, origCallID, transferCallID, reason)
}
