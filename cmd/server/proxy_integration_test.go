package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- store ---

// TestStoreProxyRoundtrip exercita getProxy/setProxy e a migration de colunas numa
// base nova (que recebe as 6 colunas ao criar o store).
func TestStoreProxyRoundtrip(t *testing.T) {
	ctx := context.Background()
	st := mustNewTestStore(t)

	id := newSessionID()
	if err := st.insert(ctx, id, "Proxy Acct"); err != nil {
		t.Fatal(err)
	}

	cfg, err := st.getProxy(ctx, id)
	if err != nil || cfg.Enabled {
		t.Fatalf("esperado proxy zerado/desabilitado em sessão nova: %+v %v", cfg, err)
	}

	want := ProxyConfig{Enabled: true, Type: "SOCKS5", Host: "proxy.exemplo.com", Port: 1080, Username: "u", Password: "s"}
	if err := st.setProxy(ctx, id, want); err != nil {
		t.Fatal(err)
	}
	got, err := st.getProxy(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled != want.Enabled || got.Type != want.Type || got.Host != want.Host ||
		got.Port != want.Port || got.Username != want.Username || got.Password != want.Password {
		t.Fatalf("roundtrip divergiu: got %+v want %+v", got, want)
	}

	if err := st.setProxy(ctx, id, ProxyConfig{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := st.getProxy(ctx, id); cfg.Enabled {
		t.Fatal("esperado Enabled=false após setar disabled")
	}
}

// TestMigrationIdempotent reabre o store contra a mesma base e confirma que a
// migration (ALTER TABLE ADD COLUMN) não falha ao rodar de novo.
func TestMigrationIdempotent(t *testing.T) {
	ctx := context.Background()
	db := mustOpenTestDB(t)
	if _, err := newSessionStore(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := newSessionStore(ctx, db); err != nil { // segundo init não deve erro nas colunas
		t.Fatalf("reabrir store falhou: %v", err)
	}
	cols, err := tableColumns(db, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"proxy_enabled", "proxy_type", "proxy_host", "proxy_port", "proxy_username", "proxy_password"} {
		if !contains(cols, want) {
			t.Fatalf("coluna %s ausente após migration", want)
		}
	}
}

// --- Session.info() & SetProxy ---

// TestSessionInfoMasksPassword confirma que info() nunca expõe a senha e que Set
// reflete a existência de senha.
func TestSessionInfoMasksPassword(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t)
	s := m.addUnconnected(t, "Mask Acct")

	if err := m.store.setProxy(ctx, s.id, ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 8080, Username: "u", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	s.setProxyInfo(ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 8080, Username: "u", Password: "secret"})

	info := s.info()
	if info.Proxy == nil {
		t.Fatal("esperado Proxy populado")
	}
	if info.Proxy.Password != "" {
		t.Fatalf("senha vazada em info(): %q", info.Proxy.Password)
	}
	if !info.Proxy.Set {
		t.Fatal("esperado proxySet=true quando há senha")
	}
}

// TestSessionInfo_NoProxyWhenDisabled confirma que sessões sem proxy devolvem Proxy nil.
func TestSessionInfo_NoProxyWhenDisabled(t *testing.T) {
	m := newTestManager(t)
	s := m.addUnconnected(t, "NoProxy")
	if s.info().Proxy != nil {
		t.Fatal("esperado Proxy nil em sessão sem proxy")
	}
}

// TestSetProxy_RejectsActiveCalls confirma o gate 409 (errProxyReconnectBlocked)
// quando há chamada ativa.
func TestSetProxy_RejectsActiveCalls(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t)
	s := m.addUnconnected(t, "Busy Acct")
	s.reg.add("fake-call", &activeCall{}) // simula chamada ativa (count() só conta len)

	err := m.SetProxy(ctx, s.id, ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 1})
	if !errors.Is(err, errProxyReconnectBlocked) {
		t.Fatalf("esperado errProxyReconnectBlocked, got %v", err)
	}
	// Config não deve ter sido persistida (gate antes do setProxy).
	if cfg, _ := m.store.getProxy(ctx, s.id); cfg.Enabled {
		t.Fatal("config não deve ser persistida quando bloqueado por chamada ativa")
	}
}

// TestSetProxy_KeepsPasswordWhenEmpty confirma que senha vazia no save preserva a atual.
// A sessão não reconecta (sem JID e não estava conectada) — teste offline-safe.
func TestSetProxy_KeepsPasswordWhenEmpty(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t)
	s := m.addUnconnected(t, "Keep Pwd Acct")

	if err := m.SetProxy(ctx, s.id, ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 8080, Username: "u", Password: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetProxy(ctx, s.id, ProxyConfig{Enabled: true, Type: "HTTP", Host: "h2", Port: 8080, Username: "u", Password: ""}); err != nil {
		t.Fatal(err)
	}
	got, _ := m.store.getProxy(ctx, s.id)
	if got.Password != "first" {
		t.Fatalf("senha não preservada: got %q want %q", got.Password, "first")
	}
	if got.Host != "h2" {
		t.Fatalf("host não atualizado: got %q want %q", got.Host, "h2")
	}
}

// TestSetProxy_NewPasswordOverwrites confirma que senha não-vazia sobrescreve.
func TestSetProxy_NewPasswordOverwrites(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t)
	s := m.addUnconnected(t, "Overwrite Pwd Acct")
	_ = m.SetProxy(ctx, s.id, ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 1, Username: "u", Password: "old"})
	_ = m.SetProxy(ctx, s.id, ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 1, Username: "u", Password: "new"})
	got, _ := m.store.getProxy(ctx, s.id)
	if got.Password != "new" {
		t.Fatalf("esperado senha sobrescrita, got %q", got.Password)
	}
}

// TestSetProxy_ValidationErrors confirma que config inválida retorna erro (400).
func TestSetProxy_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t)
	s := m.addUnconnected(t, "Bad Cfg Acct")

	for _, cfg := range []ProxyConfig{
		{Enabled: true, Type: "FTP", Host: "h", Port: 1},
		{Enabled: true, Type: "HTTP", Port: 1},
		{Enabled: true, Type: "HTTP", Host: "h", Port: 0},
		{Enabled: true, Type: "HTTP", Host: "h", Port: 70000},
	} {
		if err := m.SetProxy(ctx, s.id, cfg); err == nil {
			t.Fatalf("esperado erro para config %+v", cfg)
		}
	}
}

// TestSetProxy_NotFoundSession confirma "no session" para sid inexistente.
func TestSetProxy_NotFoundSession(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t)
	err := m.SetProxy(ctx, "inexistente", ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 1})
	if err == nil || !isNoSessionErr(err, "inexistente") {
		t.Fatalf("esperado no session, got %v", err)
	}
}

// --- handlers (httptest) ---

// TestHandleProxyRoutes agrupa os casos de handler que não dependem de rede real.
func TestHandleSetProxy_400Invalid(t *testing.T) {
	srv, sid := newTestServerWithSession(t)
	body := `{"proxyEnabled": true, "proxyType": "FTP", "proxyHost": "h", "proxyPort": 1}`
	rec := srv.do(t, http.MethodPost, "/api/sessions/"+sid+"/proxy", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperado 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHandleTestProxy_BadHost(t *testing.T) {
	srv, sid := newTestServerWithSession(t)
	// 127.0.0.1:1 — dial será recusado; probe deve falhar com success=false.
	body := `{"proxyEnabled": true, "proxyType": "HTTP", "proxyHost": "127.0.0.1", "proxyPort": 1, "timeoutMs": 1500}`
	rec := srv.do(t, http.MethodPost, "/api/sessions/"+sid+"/proxy/test", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperado 200 (resultado com success=false), got %d (%s)", rec.Code, rec.Body.String())
	}
	var res ProxyTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatalf("esperado success=false para host inalcançável, got %+v", res)
	}
	if res.ErrorCategory == "" {
		t.Fatal("esperado errorCategory não-vazio")
	}
}

func TestHandleTestProxy_400InvalidConfig(t *testing.T) {
	srv, sid := newTestServerWithSession(t)
	body := `{"proxyEnabled": true, "proxyType": "HTTP", "proxyPort": 1}` // sem host
	rec := srv.do(t, http.MethodPost, "/api/sessions/"+sid+"/proxy/test", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperado 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestHandleGetProxy_NotConfigured(t *testing.T) {
	srv, sid := newTestServerWithSession(t)
	rec := srv.do(t, http.MethodGet, "/api/sessions/"+sid+"/proxy", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperado 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var info ProxyInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Enabled {
		t.Fatal("esperado Enabled=false em sessão sem proxy")
	}
}

func TestHandleSetProxy_NotFoundSession(t *testing.T) {
	srv := newTestServer(t)
	body := `{"proxyEnabled": true, "proxyType": "HTTP", "proxyHost": "h", "proxyPort": 1}`
	rec := srv.do(t, http.MethodPost, "/api/sessions/inexistente/proxy", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperado 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestHandleGetProxy_MaskedAfterSet confirma que a senha nunca aparece em GET após salvar.
func TestHandleGetProxy_MaskedAfterSet(t *testing.T) {
	srv, sid := newTestServerWithSession(t)
	body := `{"proxyEnabled": true, "proxyType": "SOCKS5", "proxyHost": "h", "proxyPort": 1080, "proxyUsername": "u", "proxyPassword": "topsecret"}`
	rec := srv.do(t, http.MethodPost, "/api/sessions/"+sid+"/proxy", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperado 200 ao salvar, got %d (%s)", rec.Code, rec.Body.String())
	}
	rec = srv.do(t, http.MethodGet, "/api/sessions/"+sid+"/proxy", "")
	var info ProxyInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Password != "" {
		t.Fatalf("senha vazada em GET: %q", info.Password)
	}
	if !info.Set {
		t.Fatal("esperado proxySet=true após salvar senha")
	}
	if strings.Contains(rec.Body.String(), "topsecret") {
		t.Fatalf("senha encontrada no corpo do GET: %s", rec.Body.String())
	}
}

// --- test helpers ---

// mustNewTestStore abre um DB novo e devolve um sessionStore com as colunas migradas.
func mustNewTestStore(t *testing.T) *sessionStore {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "proxy_store_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := newSessionStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// mustOpenTestDB abre um DB novo (sem sessionStore) para testar a migration crua.
func mustOpenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "proxy_mig_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testServer envolve um *server. Os handlers de proxy são protegidos por withAuth
// (WACALLS_API_KEY); para os testes, fixamos essa env na key "test-key" e a enviamos
// no header, evitando a key aleatória que routes() gera quando a env está vazia.
type testServer struct {
	*server
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	os.Setenv("WACALLS_API_KEY", "test-key")
	t.Cleanup(func() { os.Unsetenv("WACALLS_API_KEY") })
	m := newTestManager(t)
	return &testServer{server: &server{broker: m.broker, sessions: m, log: slog.Default()}}
}

// newTestServerWithSession cria um servidor e uma sessão nele; devolve ambos.
func newTestServerWithSession(t *testing.T) (*testServer, string) {
	t.Helper()
	srv := newTestServer(t)
	sid := newSessionID()
	if err := srv.sessions.store.insert(srv.sessions.appCtx, sid, "Sess Teste"); err != nil {
		t.Fatal(err)
	}
	// Registra a sessão no manager (caminho de addUnconnected) para sessionByID achá-la.
	client := newWAClient(srv.sessions, srv.sessions.container.NewDevice(), sid)
	s := newSession(srv.sessions, sid, "Sess Teste", client)
	srv.sessions.register(s)
	return srv, sid
}

// do executa uma requisição contra as rotas do servidor; devolve o ResponseRecorder.
func (ts *testServer) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	handler := ts.routes() // lê WACALLS_API_KEY (fixada em newTestServer)
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

