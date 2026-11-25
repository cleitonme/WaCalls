package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// wsTicketTTL é a validade do ticket. Ele só é usado para INICIAR o socket:
// quem conecta pede o ticket e abre o WebSocket em seguida, então 60 s cobrem
// com folga o handshake sem manter uma credencial de mídia de longa duração.
const wsTicketTTL = 60 * time.Second

var errInvalidWSTicket = errors.New("ws ticket inválido")

// wsTicketKey deriva a chave de assinatura da própria WACALLS_API_KEY, sem
// variável de ambiente nova. Girar a API key invalida tickets em aberto — o
// que é o comportamento desejado.
func wsTicketKey(apiKey string) []byte {
	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte("wacalls/ws-ticket"))
	return mac.Sum(nil)
}

func wsTicketSignature(apiKey, payload string) []byte {
	mac := hmac.New(sha256.New, wsTicketKey(apiKey))
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// signWSTicket emite o ticket no formato base64url(sid|callId|exp).assinatura,
// escopado a UMA chamada. O ticket autoriza apenas a abertura do socket: a
// existência da chamada e o vínculo com a sessão continuam sendo validados por
// handleWSBridge (registry da sessão).
func signWSTicket(apiKey, sid, callID string, now time.Time) string {
	exp := strconv.FormatInt(now.Add(wsTicketTTL).Unix(), 10)
	payload := base64.RawURLEncoding.EncodeToString([]byte(sid + "|" + callID + "|" + exp))
	return payload + "." + base64.RawURLEncoding.EncodeToString(wsTicketSignature(apiKey, payload))
}

// verifyWSTicket confere assinatura (em tempo constante), escopo e validade.
func verifyWSTicket(apiKey, ticket, sid, callID string, now time.Time) error {
	payload, encodedSig, ok := strings.Cut(ticket, ".")
	if !ok || payload == "" || encodedSig == "" {
		return errInvalidWSTicket
	}

	gotSig, err := base64.RawURLEncoding.DecodeString(encodedSig)
	if err != nil {
		return errInvalidWSTicket
	}
	if !hmac.Equal(gotSig, wsTicketSignature(apiKey, payload)) {
		return errInvalidWSTicket
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return errInvalidWSTicket
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return errInvalidWSTicket
	}
	if parts[0] != sid || parts[1] != callID {
		return errInvalidWSTicket
	}

	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return errInvalidWSTicket
	}
	if now.Unix() > exp {
		return errInvalidWSTicket
	}
	return nil
}

// handleWSTicket atende POST /api/sessions/{sid}/calls/{id}/ws-ticket.
//
// Rota sob withAuth (API key), pensada para um backend intermediário (ex.:
// Whazing): ele já validou usuário/permissão, pede o ticket e entrega ao
// navegador apenas o ticket — a WACALLS_API_KEY nunca chega ao browser.
func (s *server) handleWSTicket(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	callID := r.PathValue("id")

	sess := s.sessionByID(w, sid)
	if sess == nil {
		return
	}
	if _, ok := sess.reg.get(callID); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such call"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     signWSTicket(s.apiKey, sid, callID, time.Now()),
		"expires_in": int(wsTicketTTL.Seconds()),
	})
}

// allowWSTicket aceita o ticket curto APENAS na rota do WebSocket de mídia
// (/api/sessions/{sid}/calls/{id}/ws). Nenhum outro /api/* aceita ticket, e a
// API key (header X-API-Key ou ?apiKey=) continua funcionando como sempre.
func allowWSTicket(r *http.Request, key string) bool {
	sid, callID, ok := wsBridgePath(r.URL.Path)
	if !ok {
		return false
	}
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		return false
	}
	return verifyWSTicket(key, ticket, sid, callID, time.Now()) == nil
}

// wsBridgePath reconhece exatamente /api/sessions/{sid}/calls/{id}/ws.
func wsBridgePath(p string) (sid, callID string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(p, "/api/sessions/"), "/")
	if len(parts) != 4 || parts[1] != "calls" || parts[3] != "ws" {
		return "", "", false
	}
	if parts[0] == "" || parts[2] == "" || !strings.HasPrefix(p, "/api/sessions/") {
		return "", "", false
	}
	return parts[0], parts[2], true
}
