package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestWSTicketRoundTrip(t *testing.T) {
	now := time.Now()
	ticket := signWSTicket("api-key", "sid1", "call1", now)

	if err := verifyWSTicket("api-key", ticket, "sid1", "call1", now); err != nil {
		t.Fatalf("ticket válido rejeitado: %v", err)
	}
}

func TestWSTicketRejectsWrongCall(t *testing.T) {
	now := time.Now()
	ticket := signWSTicket("api-key", "sid1", "call1", now)

	if err := verifyWSTicket("api-key", ticket, "sid1", "call2", now); err == nil {
		t.Fatal("ticket de outra chamada foi aceito")
	}
	if err := verifyWSTicket("api-key", ticket, "sid2", "call1", now); err == nil {
		t.Fatal("ticket de outra sessão foi aceito")
	}
}

func TestWSTicketRejectsWrongKey(t *testing.T) {
	now := time.Now()
	ticket := signWSTicket("api-key", "sid1", "call1", now)

	if err := verifyWSTicket("outra-key", ticket, "sid1", "call1", now); err == nil {
		t.Fatal("ticket assinado com outra key foi aceito")
	}
}

func TestWSTicketExpires(t *testing.T) {
	issuedAt := time.Now().Add(-2 * time.Minute)
	ticket := signWSTicket("api-key", "sid1", "call1", issuedAt)

	if err := verifyWSTicket("api-key", ticket, "sid1", "call1", time.Now()); err == nil {
		t.Fatal("ticket expirado foi aceito")
	}
}

func TestWSTicketRejectsTamperedPayload(t *testing.T) {
	now := time.Now()
	ticket := signWSTicket("api-key", "sid1", "call1", now)

	if err := verifyWSTicket("api-key", "x"+ticket, "sid1", "call1", now); err == nil {
		t.Fatal("ticket adulterado foi aceito")
	}
	if err := verifyWSTicket("api-key", ticket[:len(ticket)-2], "sid1", "call1", now); err == nil {
		t.Fatal("ticket truncado foi aceito")
	}
}

func TestAllowWSTicketOnlyOnWSBridge(t *testing.T) {
	ticket := url.QueryEscape(signWSTicket("k", "sid1", "call1", time.Now()))

	ws := httptest.NewRequest(http.MethodGet, "/api/sessions/sid1/calls/call1/ws?ticket="+ticket, nil)
	if !allowWSTicket(ws, "k") {
		t.Fatal("ticket válido recusado na rota do WebSocket")
	}

	// Nenhuma outra rota /api/* pode aceitar ticket.
	for _, p := range []string{
		"/api/sessions/sid1/calls/call1/hold?ticket=" + ticket,
		"/api/sessions/sid1/proxy?ticket=" + ticket,
		"/api/events?ticket=" + ticket,
		"/api/config?ticket=" + ticket,
	} {
		if allowWSTicket(httptest.NewRequest(http.MethodGet, p, nil), "k") {
			t.Fatalf("rota %q aceitou ticket", p)
		}
	}

	// Assinatura de outra key continua inválida.
	if allowWSTicket(ws, "outra-key") {
		t.Fatal("ticket foi aceito com outra key")
	}
}

func TestWSBridgePath(t *testing.T) {
	sid, callID, ok := wsBridgePath("/api/sessions/abc/calls/def/ws")
	if !ok || sid != "abc" || callID != "def" {
		t.Fatalf("caminho válido não reconhecido: %q %q %v", sid, callID, ok)
	}

	for _, p := range []string{
		"/api/sessions/abc/calls/def",
		"/api/sessions/abc/calls/def/ws/x",
		"/api/sessions/abc/ws",
		"/api/sessions//calls/def/ws",
		"/api/config",
	} {
		if _, _, ok := wsBridgePath(p); ok {
			t.Fatalf("caminho %q aceito indevidamente", p)
		}
	}
}
