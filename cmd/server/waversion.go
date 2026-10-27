package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/store"
)

// Fontes remotas na mesma ordem usada pelo WuzAPI.
// A primeira válida vence; se todas falharem, usa-se a versão embutida do whatsmeow.
var defaultWAVersionURLs = []string{
	"https://raw.githubusercontent.com/cleitonme/Whazing-SaaS/refs/heads/main/waversion.json",
	"https://scripts.infomeurer.com.br/waversion.json",
}

const (
	waVersionTTL     = 24 * time.Hour
	waVersionTimeout = 10 * time.Second
	waVersionMaxBody = 64
)

// parseWAVersionURLs converte a lista separada por vírgula de -wa-version-url /
// WACALLS_WA_VERSION_URL, ignorando espaços e entradas vazias.
func parseWAVersionURLs(raw string) []string {
	var urls []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			urls = append(urls, p)
		}
	}
	return urls
}

// waVersionResolver busca, cacheia e aplica a versão do WhatsApp Web.
//
// base é capturada de store.GetWAVersion() na construção e serve de piso: a
// versão remota só é aplicada se for maior. Isso evita downgrade caso o JSON
// remoto fique atrás da versão embutida no whatsmeow (a lib recomenda manter-se
// atualizada em vez de forçar SetWAVersion).
type waVersionResolver struct {
	urls []string
	base store.WAVersionContainer

	client *http.Client
	ttl    time.Duration
	now    func() time.Time
	log    *slog.Logger

	mu        sync.Mutex
	cached    store.WAVersionContainer
	fetchedAt time.Time
}

func newWAVersionResolver(log *slog.Logger, urls []string) *waVersionResolver {
	if len(urls) == 0 {
		urls = defaultWAVersionURLs
	}
	return &waVersionResolver{
		urls:   urls,
		base:   store.GetWAVersion(),
		client: &http.Client{Timeout: waVersionTimeout},
		ttl:    waVersionTTL,
		now:    time.Now,
		log:    log,
	}
}

func parseWAVersion(data []byte) (store.WAVersionContainer, error) {
	var parts []uint32
	if err := json.Unmarshal(data, &parts); err != nil {
		return store.WAVersionContainer{}, fmt.Errorf("invalid waversion.json: %w", err)
	}
	if len(parts) != 3 {
		return store.WAVersionContainer{}, fmt.Errorf("invalid waversion.json: expected 3 parts, got %d", len(parts))
	}
	return store.WAVersionContainer{parts[0], parts[1], parts[2]}, nil
}

// greaterWAVersion retorna a maior entre a e b (empate retorna a), equivalente
// ao getGreaterVersion do exemplo Baileys usado pelo WuzAPI.
func greaterWAVersion(a, b store.WAVersionContainer) store.WAVersionContainer {
	if a.LessThan(b) {
		return b
	}
	return a
}

// fetch tenta cada fonte em ordem e devolve a primeira versão válida.
func (r *waVersionResolver) fetch(ctx context.Context) (store.WAVersionContainer, string, bool) {
	for _, url := range r.urls {
		reqCtx, cancel := context.WithTimeout(ctx, waVersionTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			r.log.Warn("falha ao preparar requisição da versão do WhatsApp", "url", url, "err", err)
			continue
		}
		resp, err := r.client.Do(req)
		if err != nil {
			cancel()
			r.log.Warn("falha ao buscar versão do WhatsApp", "url", url, "err", err)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, waVersionMaxBody))
		status := resp.StatusCode
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			r.log.Warn("falha ao ler corpo da versão do WhatsApp", "url", url, "err", readErr)
			continue
		}
		if status != http.StatusOK {
			r.log.Warn("resposta inesperada ao buscar versão do WhatsApp", "url", url, "status", status)
			continue
		}
		parsed, err := parseWAVersion(body)
		if err != nil {
			r.log.Warn("payload inválido de versão do WhatsApp", "url", url, "err", err)
			continue
		}
		r.log.Debug("versão do WhatsApp obtida", "url", url, "version", parsed.String())
		return parsed, url, true
	}
	return store.WAVersionContainer{}, "", false
}

// Resolve resolve, cacheia e aplica (store.SetWAVersion) a versão a usar.
// Deve ser chamado antes de client.Connect/GetQRChannel, pois a versão entra no
// payload de handshake. Idempotente e seguro para connects concorrentes.
func (r *waVersionResolver) Resolve(ctx context.Context) store.WAVersionContainer {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.cached.IsZero() && r.now().Sub(r.fetchedAt) < r.ttl {
		store.SetWAVersion(r.cached)
		r.log.Debug("versão do WhatsApp em cache", "version", r.cached.String())
		return r.cached
	}

	final := r.base
	source := "whatsmeow"
	var sourceURL string

	remote, url, ok := r.fetch(ctx)
	if ok {
		final = greaterWAVersion(r.base, remote)
		if r.base.LessThan(remote) {
			source = "remote"
			sourceURL = url
		} else {
			r.log.Debug("versão remota não supera a embutida no whatsmeow", "remote", remote.String(), "local", r.base.String(), "url", url)
		}
	} else {
		r.log.Warn("não foi possível buscar a versão do WhatsApp; usando a versão embutida do whatsmeow")
	}

	store.SetWAVersion(final)
	r.cached = final
	r.fetchedAt = r.now()
	r.log.Info("versão do WhatsApp em uso", "version", final.String(), "source", source, "url", sourceURL)
	return final
}
