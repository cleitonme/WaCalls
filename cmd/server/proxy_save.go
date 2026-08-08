package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// errProxyReconnectBlocked indica que o proxy não pôde ser reaplicado porque há
// chamadas ativas na sessão (a reconexão as derrubaria).
var errProxyReconnectBlocked = errors.New("session has active calls; hang up before changing proxy")

// SetProxy valida, persiste e aplica a configuração de proxy de um canal. Como
// SetProxyAddress só vale antes de Connect(), aplicar um proxy novo exige
// desconectar e reconectar o cliente — daí o gate de chamadas ativas (409).
//
// Retorno de erros:
//   - ProxyConfig inválida      -> erro de validação (400 no handler)
//   - sessão inexistente        -> "no session <sid>" (404)
//   - chamadas ativas           -> errProxyReconnectBlocked (409)
//   - falha de persistência     -> erro (500)
func (m *SessionManager) SetProxy(ctx context.Context, sid string, cfg ProxyConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	s, ok := m.Get(sid)
	if !ok {
		return fmt.Errorf("no session %s", sid)
	}

	// Gate: não reconecta com chamadas em andamento.
	if s.reg.count() > 0 {
		return errProxyReconnectBlocked
	}

	// A senha nunca é ecoada de volta (info() mascara), então o formulário envia
	// senha vazia quando o operador não a altera: preservamos a atual nesse caso.
	if cfg.Password == "" {
		if old, err := m.store.getProxy(ctx, sid); err == nil {
			cfg.Password = old.Password
		}
	}

	if err := m.store.setProxy(ctx, sid, cfg); err != nil {
		return fmt.Errorf("persist proxy: %w", err)
	}
	s.setProxyInfo(cfg)

	// Reaplica o proxy reconstruindo o cliente. Preserva a identidade pareada
	// (reusa o mesmo device) e só reconecta se a sessão estava conectada ou já
	// tinha JID — evita emitir QR surpresa numa sessão intencionalmente offline.
	m.log.Info("[Proxy] reconectando canal para aplicar novo proxy", "session", sid, "type", cfg.Type, "host", cfg.Host, "port", cfg.Port, "auth", cfg.Username != "", "enabled", cfg.Enabled)
	device := s.client.Store
	wasConnected := s.client.IsConnected()
	hadJID := s.client.Store.ID != nil
	s.replaceClient(newWAClient(m, device, sid))
	if wasConnected || hadJID {
		if err := s.connect(ctx); err != nil {
			// A config foi persistida; a sessão apenas ficou desconectada.
			m.log.Error("[Proxy] reconexão falhou após mudança de proxy", "session", sid, "err", err)
		}
	}

	m.broker.emitSessionList(m.infos())
	return nil
}

// GetProxy retorna a configuração mascarada de proxy de um canal (para GET).
// Sessões sem proxy devolvem ProxyInfo zerada (Enabled=false).
func (m *SessionManager) GetProxy(ctx context.Context, sid string) (ProxyInfo, error) {
	cfg, err := m.store.getProxy(ctx, sid)
	if err != nil {
		return ProxyInfo{}, err
	}
	return cfg.masked(), nil
}

// isNoSessionErr reconhece o erro "no session <sid>" emitido por SetProxy quando
// a sessão não existe, para mapeá-lo a 404 no handler.
func isNoSessionErr(err error, sid string) bool {
	return err != nil && strings.Contains(err.Error(), "no session") && strings.Contains(err.Error(), sid)
}
