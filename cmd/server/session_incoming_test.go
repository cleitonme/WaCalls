package main

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

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

func newTestSessionManager(t *testing.T, maxCalls int) *SessionManager {
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
	return newSessionManager(ctx, container, NewBroker(), store, waLog.Noop, slog.Default(), maxCalls, nil)
}