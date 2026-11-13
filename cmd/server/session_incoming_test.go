package main

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"wacalls/internal/voip/core"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestOnIncomingOfferRespectsMaxCalls(t *testing.T) {
	mgr := newTestSessionManager(t, 2)

	sess := mgr.addUnconnected(t, "test-session")

	peerJID, err := types.ParseJID("1234567890@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}

	callID := "call-123"
	offerNode := &waBinary.Node{
		Tag: "offer",
		Attrs: waBinary.Attrs{
			"call-id": callID,
		},
	}
	node := wrapCall(peerJID, offerNode)

	mgr.mu.Lock()
	mgr.maxCalls = 0
	mgr.mu.Unlock()

	sess.onIncomingOfferDirect(context.Background(), peerJID, node)

	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call after offer with maxCalls=0, got %d", sess.reg.count())
	}

	sess.reg.remove(callID)
	if sess.reg.count() != 0 {
		t.Fatalf("expected 0 calls after remove, got %d", sess.reg.count())
	}

	mgr.mu.Lock()
	mgr.maxCalls = 1
	mgr.mu.Unlock()

	sess.onIncomingOfferDirect(context.Background(), peerJID, node)
	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call after offer with maxCalls=1, got %d", sess.reg.count())
	}

	sess.reg.add("call-other", &activeCall{})
	if sess.reg.count() != 2 {
		t.Fatalf("expected 2 calls in registry before limit test, got %d", sess.reg.count())
	}

	sess.onIncomingOfferDirect(context.Background(), peerJID, node)

	if sess.reg.count() != 2 {
		t.Fatalf("expected call to be ignored when at capacity, got %d calls", sess.reg.count())
	}
}

func TestOnIncomingOfferDoesNotInterruptExistingCall(t *testing.T) {
	mgr := newTestSessionManager(t, 1)

	sess := mgr.addUnconnected(t, "test-session")

	peerJID, err := types.ParseJID("1234567890@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}

	callID := "call-active"
	offerNode := &waBinary.Node{
		Tag: "offer",
		Attrs: waBinary.Attrs{
			"call-id": callID,
		},
	}
	node := wrapCall(peerJID, offerNode)

	sess.onIncomingOfferDirect(context.Background(), peerJID, node)

	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call, got %d", sess.reg.count())
	}

	if _, ok := sess.reg.get(callID); !ok {
		t.Fatal("call should exist in registry")
	}

	sess.reg.add("call-new", &activeCall{})
	if sess.reg.count() != 2 {
		t.Fatalf("expected 2 calls in registry before limit test, got %d", sess.reg.count())
	}

	sess.onIncomingOfferDirect(context.Background(), peerJID, &waBinary.Node{
		Tag: "offer",
		Attrs: waBinary.Attrs{"call-id": "call-dropped"},
	})

	if sess.reg.count() != 2 {
		t.Fatalf("expected 2 calls after ignoring new offer, got %d", sess.reg.count())
	}

	if _, ok := sess.reg.get(callID); !ok {
		t.Fatal("original active call should still exist in registry")
	}
}

func TestIncomingCallTimeout(t *testing.T) {
	origTimeout := ringingTimeout
	ringingTimeout = 100 * time.Millisecond
	defer func() { ringingTimeout = origTimeout }()

	b := NewBroker()
	mgr := newTestSessionManagerWithBroker(t, 1, b)

	sess := mgr.addUnconnected(t, "test-session")

	peerJID, err := types.ParseJID("1234567890@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}

	callID := "call-timeout"
	offerNode := &waBinary.Node{
		Tag: "offer",
		Attrs: waBinary.Attrs{
			"call-id": callID,
		},
	}
	node := wrapCall(peerJID, offerNode)

	sess.onIncomingOfferDirect(context.Background(), peerJID, node)

	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call after offer, got %d", sess.reg.count())
	}

	time.Sleep(150 * time.Millisecond)

	if sess.reg.count() != 0 {
		t.Fatalf("expected 0 calls after timeout, got %d", sess.reg.count())
	}

	recs := b.historyRows("", 10)
	var found bool
	for _, rec := range recs {
		if rec.CallID == callID {
			found = true
			if rec.Status != StatusEnded {
				t.Fatalf("expected call status to be ended, got %s", rec.Status)
			}
			if rec.EndReason != string(core.EndCallReasonTimeout) {
				t.Fatalf("expected end reason to be timeout, got %s", rec.EndReason)
			}
			break
		}
	}
	if !found {
		t.Fatal("call record should exist in broker history")
	}
}

func TestIncomingCallTimeoutDoesNotAffectConnectedCall(t *testing.T) {
	origTimeout := ringingTimeout
	ringingTimeout = 100 * time.Millisecond
	defer func() { ringingTimeout = origTimeout }()

	b := NewBroker()
	mgr := newTestSessionManagerWithBroker(t, 1, b)

	sess := mgr.addUnconnected(t, "test-session")

	peerJID, err := types.ParseJID("1234567890@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}

	callID := "call-connected"
	offerNode := &waBinary.Node{
		Tag: "offer",
		Attrs: waBinary.Attrs{
			"call-id": callID,
		},
	}
	node := wrapCall(peerJID, offerNode)

	sess.onIncomingOfferDirect(context.Background(), peerJID, node)

	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call after offer, got %d", sess.reg.count())
	}

	b.setOwner(callID, "test-session")
	b.setCallStatus(callID, StatusConnected)

	time.Sleep(50 * time.Millisecond)

	if sess.reg.count() != 1 {
		t.Fatalf("expected call to remain in registry (connected), got %d", sess.reg.count())
	}

	rec, ok := b.getCall(callID)
	if !ok {
		t.Fatal("call record should still exist in broker")
	}
	if rec.Status != StatusConnected {
		t.Fatalf("expected call status to remain connected, got %s", rec.Status)
	}

	time.Sleep(150 * time.Millisecond)

	if sess.reg.count() != 1 {
		t.Fatalf("expected call to still be in registry after timeout period, got %d", sess.reg.count())
	}
}

func TestIncomingCallTimeoutNotProcessedAfterCallEnded(t *testing.T) {
	origTimeout := ringingTimeout
	ringingTimeout = 200 * time.Millisecond
	defer func() { ringingTimeout = origTimeout }()

	mgr := newTestSessionManager(t, 1)

	sess := mgr.addUnconnected(t, "test-session")

	peerJID, err := types.ParseJID("1234567890@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}

	callID := "call-rejected"
	offerNode := &waBinary.Node{
		Tag: "offer",
		Attrs: waBinary.Attrs{"call-id": callID},
	}
	node := wrapCall(peerJID, offerNode)

	sess.onIncomingOfferDirect(context.Background(), peerJID, node)

	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call after offer, got %d", sess.reg.count())
	}

	ac, ok := sess.reg.get(callID)
	if !ok {
		t.Fatal("call should exist in registry")
	}

	if ac.cm == nil {
		t.Fatal("call manager should exist")
	}

	_ = ac.cm.RejectCall(context.Background(), callID, core.EndCallReasonDeclined)

	time.Sleep(100 * time.Millisecond)

	if sess.reg.count() != 0 {
		t.Fatalf("expected 0 calls after reject, got %d", sess.reg.count())
	}

	time.Sleep(200 * time.Millisecond)

	if sess.reg.count() != 0 {
		t.Fatalf("expected 0 calls after timeout (no double processing), got %d", sess.reg.count())
	}
}

func TestIncomingCallTimeoutDoesNotAffectOtherCalls(t *testing.T) {
	origTimeout := ringingTimeout
	ringingTimeout = 200 * time.Millisecond
	defer func() { ringingTimeout = origTimeout }()

	b := NewBroker()
	mgr := newTestSessionManagerWithBroker(t, 0, b)

	sess := mgr.addUnconnected(t, "test-session")

	peerJID1, err := types.ParseJID("1234567890@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}

	callID1 := "call-timeout-1"
	offerNode1 := &waBinary.Node{
		Tag: "offer",
		Attrs: waBinary.Attrs{"call-id": callID1},
	}
	node1 := wrapCall(peerJID1, offerNode1)

	sess.onIncomingOfferDirect(context.Background(), peerJID1, node1)

	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call after first offer, got %d", sess.reg.count())
	}

	time.Sleep(50 * time.Millisecond)

	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call to still be ringing, got %d", sess.reg.count())
	}

	b.setOwner(callID1, "test-session")
	b.setCallStatus(callID1, StatusConnected)

	callID2 := "call-timeout-2"
	offerNode2 := &waBinary.Node{
		Tag: "offer",
		Attrs: waBinary.Attrs{"call-id": callID2},
	}
	node2 := wrapCall(peerJID1, offerNode2)

	sess.onIncomingOfferDirect(context.Background(), peerJID1, node2)

	if sess.reg.count() != 2 {
		t.Fatalf("expected 2 calls after second offer, got %d", sess.reg.count())
	}

	rec, ok := b.getCall(callID1)
	if !ok {
		t.Fatal("first call should still be in active calls")
	}
	if rec.Status != StatusConnected {
		t.Fatalf("expected first call status to remain connected, got %s", rec.Status)
	}

	time.Sleep(50 * time.Millisecond)

	if sess.reg.count() != 2 {
		t.Fatalf("expected 2 calls to coexist, got %d", sess.reg.count())
	}

	time.Sleep(200 * time.Millisecond)

	if sess.reg.count() != 1 {
		t.Fatalf("expected 1 call after timeout (second timed out), got %d", sess.reg.count())
	}

	rec, ok = b.getCall(callID1)
	if !ok {
		t.Fatal("first call should still exist in active calls (connected)")
	}
	if rec.Status != StatusConnected {
		t.Fatalf("expected first call status to remain connected, got %s", rec.Status)
	}
}

func newTestSessionManager(t *testing.T, maxCalls int) *SessionManager {
	return newTestSessionManagerWithBroker(t, maxCalls, NewBroker())
}

func newTestSessionManagerWithBroker(t *testing.T, maxCalls int, broker *Broker) *SessionManager {
	t.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "mgr_test.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	container := sqlstore.NewWithDB(db, "sqlite3", waLog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := newSessionStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return newSessionManager(ctx, container, broker, store, waLog.Noop, slog.Default(), maxCalls, nil)
}

func (b *Broker) setCallStatus(id string, status CallStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.calls[id]; ok {
		c.Status = status
	}
}