package main

import (
	"context"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
)

// newWAClient cria um cliente whatsmeow e, se o canal tiver proxy ativo e válido,
// aplica-o ANTES de devolver o cliente. SetProxyAddress só tem efeito se chamado
// antes de Connect() (ver go.mau.fi/whatsmeow client.go:SetProxy), por isso a
// aplicação ocorre aqui — todos os pontos de criação passam por este helper.
//
// Segurança: NUNCA chama SetProxyAddress("") quando o proxy está desabilitado.
// Isso ativaria o fallback para a variável de ambiente https_proxy (client.go:345)
// e uma sessão sem proxy passaria misteriosamente por um proxy de ambiente.
//
// Resiliência: em qualquer falha (leitura do banco, config inválida, erro de
// SetProxyAddress) apenas loga em WARN e devolve o cliente sem proxy — nunca
// quebra a criação da sessão.
func newWAClient(m *SessionManager, device *store.Device, sid string) *whatsmeow.Client {
	cli := whatsmeow.NewClient(device, m.waLogger)

	cfg, err := m.store.getProxy(context.Background(), sid)
	if err != nil {
		m.log.Warn("[Proxy] falha ao ler config do proxy; cliente sem proxy", "session", sid, "err", err)
		return cli
	}
	if !cfg.Enabled {
		return cli // sem proxy; NÃO chamar SetProxyAddress("")
	}

	proxyURL, err := buildProxyURL(cfg)
	if err != nil {
		m.log.Warn("[Proxy] config inválida; cliente sem proxy", "session", sid, "type", cfg.Type, "host", cfg.Host, "port", cfg.Port, "err", err)
		return cli
	}

	if err := cli.SetProxyAddress(proxyURL); err != nil {
		m.log.Warn("[Proxy] SetProxyAddress falhou; cliente sem proxy", "session", sid, "type", cfg.Type, "host", cfg.Host, "port", cfg.Port, "err", err)
		return cli
	}

	m.log.Info("[Proxy] aplicado ao canal", "session", sid, "type", cfg.Type, "host", cfg.Host, "port", cfg.Port, "auth", cfg.Username != "")
	return cli
}
