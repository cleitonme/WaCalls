package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ProxyTestResult é o resultado do endpoint de teste de proxy. ErrorCategory é
// vazio em sucesso; em falha classifica o problema para o frontend exibir uma
// mensagem amigável (timeout / auth / host inválido / etc.).
type ProxyTestResult struct {
	Success       bool   `json:"success"`
	LatencyMs     int64  `json:"latencyMs"`
	ExitIP        string `json:"exitIP"`
	ErrorCategory string `json:"errorCategory"`
	Message       string `json:"message,omitempty"`
}

// probeProxy testa a reachabilidade de um proxy fazendo um HTTPS GET curto
// através dele. Não sobe cliente whatsmeow (evita handshake completo e risco de
// rate-limit/ban). Para SOCKS5 usa golang.org/x/net/proxy.FromURL (mesma lib do
// whatsmeow); para HTTP/HTTPS usa http.ProxyURL. Obtém o IP de saída via
// api.ipify.org e, como fallback de reachabilidade, web.whatsapp.com.
//
// Segurança: nunca loga a proxyURL (contém a senha). Loggers chamadores devem
// registrar apenas host:port e a categoria.
func probeProxy(ctx context.Context, cfg ProxyConfig, timeout time.Duration) ProxyTestResult {
	if err := cfg.Validate(); err != nil {
		return ProxyTestResult{Success: false, ErrorCategory: "invalid", Message: err.Error()}
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	proxyURL, err := buildProxyURL(cfg)
	if err != nil {
		return ProxyTestResult{Success: false, ErrorCategory: "invalid", Message: err.Error()}
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return ProxyTestResult{Success: false, ErrorCategory: "invalid", Message: err.Error()}
	}

	transport, err := proxyTransport(parsed)
	if err != nil {
		return ProxyTestResult{Success: false, ErrorCategory: "scheme_invalid", Message: err.Error()}
	}

	client := &http.Client{Transport: transport, Timeout: timeout}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	exitIP, err := fetchExitIP(cctx, client)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return ProxyTestResult{Success: false, LatencyMs: latency, ErrorCategory: categorizeProxyError(err), Message: err.Error()}
	}
	return ProxyTestResult{Success: true, LatencyMs: latency, ExitIP: exitIP}
}

// proxyTransport constrói um http.Transport que diala através do proxy conforme o scheme.
func proxyTransport(parsed *url.URL) (*http.Transport, error) {
	if parsed.Scheme == "socks5" {
		dialer, err := proxy.FromURL(parsed, proxy.Direct)
		if err != nil {
			return nil, err
		}
		cd, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("socks5 dialer não implementa ContextDialer")
		}
		return &http.Transport{DialContext: cd.DialContext}, nil
	}
	return &http.Transport{Proxy: http.ProxyURL(parsed)}, nil
}

// fetchExitIP pede https://api.ipify.org (devolve o IP de saída). Se falhar por
// razões não-fatais de eco, testa a reachabilidade do WhatsApp como fallback.
func fetchExitIP(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "wacalls-proxy-probe/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 5 {
		return "", &proxyHTTPError{code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		// Mesmo sem ler o corpo, o caminho proxy+TLS funcionou.
		return "", nil
	}
	return strings.TrimSpace(string(body)), nil
}

type proxyHTTPError struct{ code int }

func (e *proxyHTTPError) Error() string { return "proxy http error: " + itoa(e.code) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// categorizeProxyError mapeia erros de rede/HTTP em categorias estáveis para o frontend.
func categorizeProxyError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	if strings.Contains(msg, "407") {
		return "auth"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "host_invalid"
	}
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "lookup ") {
		return "host_invalid"
	}
	var httpErr *proxyHTTPError
	if errors.As(err, &httpErr) && httpErr.code/100 == 5 {
		return "http_error"
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "reset") || strings.Contains(msg, "connect: ") {
		return "connection_failed"
	}
	return "unknown"
}
