//go:build proxynet

package main

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Testes de probe que dependem de proxies reais (HTTP/SOCKS5) via variáveis de
// ambiente. Ficam atrás do build tag `proxynet` + env `WACALLS_PROXY_NETTEST`
// para que `go test ./...` (CI/default) nunca flaquee offline.
//
// Rodar manualmente:
//
//	WACALLS_PROXY_NETTEST=1 WACALLS_TEST_PROXY_HTTP=http://user:pass@host:port \
//	  go test -tags=proxynet ./cmd/server/ -run TestProbe -v

func TestProbeHTTP(t *testing.T) {
	addr := os.Getenv("WACALLS_TEST_PROXY_HTTP")
	if os.Getenv("WACALLS_PROXY_NETTEST") == "" || addr == "" {
		t.Skip("skipping network probe; set WACALLS_PROXY_NETTEST=1 and WACALLS_TEST_PROXY_HTTP")
	}
	cfg := mustParseProxyAddr(t, addr)
	res := probeProxy(context.Background(), cfg, 15*time.Second)
	t.Logf("result: %+v", res)
	if !res.Success && res.ErrorCategory == "scheme_invalid" {
		t.Fatalf("scheme_invalid indica bug, não blip de rede: %+v", res)
	}
}

func TestProbeSOCKS5(t *testing.T) {
	addr := os.Getenv("WACALLS_TEST_PROXY_SOCKS5")
	if os.Getenv("WACALLS_PROXY_NETTEST") == "" || addr == "" {
		t.Skip("skipping network probe; set WACALLS_PROXY_NETTEST=1 and WACALLS_TEST_PROXY_SOCKS5")
	}
	cfg := mustParseProxyAddr(t, addr)
	res := probeProxy(context.Background(), cfg, 15*time.Second)
	t.Logf("result: %+v", res)
	if !res.Success && res.ErrorCategory == "scheme_invalid" {
		t.Fatalf("scheme_invalid indica bug, não blip de rede: %+v", res)
	}
}

// mustParseProxyAddr converte um endereço "scheme://[user:pass@]host:port" em
// ProxyConfig para os testes de rede. Falha o teste se não for parseável.
func mustParseProxyAddr(t *testing.T, addr string) ProxyConfig {
	t.Helper()
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatalf("endereço de proxy inválido %q: %v", addr, err)
	}
	cfg := ProxyConfig{Enabled: true, Host: u.Hostname()}
	switch strings.ToLower(u.Scheme) {
	case "https":
		cfg.Type = "HTTPS"
	case "socks5":
		cfg.Type = "SOCKS5"
	default:
		cfg.Type = "HTTP"
	}
	if port := u.Port(); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.Port = p
		}
	}
	if u.User != nil {
		cfg.Username = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}
	return cfg
}
