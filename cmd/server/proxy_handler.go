package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// handleGetProxy devolve a configuração mascarada de proxy de um canal
// (proxyPassword sempre ""; proxySet indica se há senha armazenada).
//
// GET /api/sessions/{sid}/proxy
func (s *server) handleGetProxy(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	if s.sessionByID(w, sid) == nil {
		return
	}
	info, err := s.sessions.GetProxy(r.Context(), sid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleSetProxy salva a configuração de proxy e dispara a reconexão para valer.
//
// POST /api/sessions/{sid}/proxy  corpo: ProxyConfig (senha vazia = preservar atual)
//
// Códigos: 200 (ok), 400 (config inválida), 404 (sessão), 409 (chamadas ativas), 500 (erro interno).
func (s *server) handleSetProxy(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	if s.sessionByID(w, sid) == nil {
		return
	}
	var cfg ProxyConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := s.sessions.SetProxy(r.Context(), sid, cfg); err != nil {
		switch {
		case errors.Is(err, errProxyReconnectBlocked):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case isNoSessionErr(err, sid):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case cfg.Validate() != nil:
			// Validate falhou dentro de SetProxy → 400.
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTestProxy testa a reachabilidade de um proxy SEM salvar. Aceita um
// ProxyConfig no corpo (para testar uma mudança antes de aplicar) ou, vazio,
// testa a config já persistida. Retorna latência, IP de saída e categoria.
//
// POST /api/sessions/{sid}/proxy/test  corpo: ProxyConfig (+ timeoutMs opcional)
//
// Códigos: 200 (sempre — o resultado diz success true/false), 400 (config inválida), 404 (sessão).
func (s *server) handleTestProxy(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	if s.sessionByID(w, sid) == nil {
		return
	}
	var body struct {
		ProxyConfig `json:",inline"`
		TimeoutMs   int `json:"timeoutMs"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	cfg := body.ProxyConfig

	// Corpo vazio: testa a config persistida (com a senha real, lida do store).
	if cfg.Host == "" && cfg.Port == 0 && cfg.Type == "" && !cfg.Enabled {
		raw, err := s.sessions.store.getProxy(r.Context(), sid)
		if err != nil || !raw.Enabled {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no proxy configured"})
			return
		}
		cfg = raw
	}

	if err := cfg.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	timeout := 10 * time.Second
	if body.TimeoutMs > 0 {
		timeout = time.Duration(body.TimeoutMs) * time.Millisecond
		if timeout > 30*time.Second {
			timeout = 30 * time.Second
		}
	}
	result := probeProxy(r.Context(), cfg, timeout)
	writeJSON(w, http.StatusOK, result)
}
