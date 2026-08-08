package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestProxyValidate_Disabled(t *testing.T) {
	// Disabled é sempre válido, mesmo com campos vazios.
	if err := (ProxyConfig{Enabled: false}).Validate(); err != nil {
		t.Fatalf("disabled proxy should be valid, got %v", err)
	}
}

func TestProxyValidate_Types(t *testing.T) {
	for _, typ := range []string{"HTTP", "HTTPS", "SOCKS5", "http", "https", "socks5"} {
		c := ProxyConfig{Enabled: true, Type: typ, Host: "proxy.exemplo.com", Port: 1080}
		if err := c.Validate(); err != nil {
			t.Fatalf("type %q should be valid: %v", typ, err)
		}
	}
}

func TestProxyValidate_InvalidType(t *testing.T) {
	c := ProxyConfig{Enabled: true, Type: "FTP", Host: "h", Port: 1}
	if err := c.Validate(); err == nil {
		t.Fatal("invalid type should error")
	}
}

func TestProxyValidate_EmptyHost(t *testing.T) {
	c := ProxyConfig{Enabled: true, Type: "HTTP", Port: 8080}
	if err := c.Validate(); err == nil {
		t.Fatal("empty host when enabled should error")
	}
}

func TestProxyValidate_Port(t *testing.T) {
	for _, port := range []int{0, -1, 65536} {
		c := ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: port}
		if err := c.Validate(); err == nil {
			t.Fatalf("port %d should error", port)
		}
	}
	for _, port := range []int{1, 80, 1080, 65535} {
		c := ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: port}
		if err := c.Validate(); err != nil {
			t.Fatalf("port %d should be valid: %v", port, err)
		}
	}
}

func TestBuildProxyURL_Schemes(t *testing.T) {
	cases := []struct {
		typ   string
		want  string
	}{
		{"HTTP", "http://h:8080"},
		{"HTTPS", "https://h:443"},
		{"SOCKS5", "socks5://h:1080"},
	}
	for _, c := range cases {
		got, err := buildProxyURL(ProxyConfig{Enabled: true, Type: c.typ, Host: "h", Port: portOf(c.want)})
		if err != nil {
			t.Fatalf("type %q: %v", c.typ, err)
		}
		u, _ := url.Parse(got)
		if u.Scheme+"://"+u.Host != c.want {
			t.Errorf("type %q: got %s, want %s", c.typ, u.Scheme+"://"+u.Host, c.want)
		}
	}
}

func portOf(u string) int {
	i := strings.LastIndex(u, ":")
	n := 0
	for ; i < len(u); i++ {
		if u[i] >= '0' && u[i] <= '9' {
			n = n*10 + int(u[i]-'0')
		}
	}
	return n
}

func TestBuildProxyURL_NoAuth(t *testing.T) {
	got, err := buildProxyURL(ProxyConfig{Enabled: true, Type: "HTTP", Host: "proxy.exemplo.com", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	if u.User != nil {
		t.Fatalf("expected no userinfo, got %v", u.User)
	}
}

func TestBuildProxyURL_WithAuth(t *testing.T) {
	got, err := buildProxyURL(ProxyConfig{Enabled: true, Type: "HTTP", Host: "proxy.exemplo.com", Port: 8080, Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	if u.User == nil || u.User.Username() != "user" {
		t.Fatalf("missing/invalid user: %v", u.User)
	}
	if pw, ok := u.User.Password(); !ok || pw != "pass" {
		t.Fatalf("missing/invalid password: %v", ok)
	}
}

func TestBuildProxyURL_EncodesSpecialChars(t *testing.T) {
	// user/senha com espaços e ':'/'@' devem ser percent-encodados.
	got, err := buildProxyURL(ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 1, Username: "a b", Password: "p@ss:w"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	// A senha codifica '@' como %40, ':' como %3A, espaço como %20.
	if !strings.Contains(got, "%40") || !strings.Contains(got, "%3A") {
		t.Fatalf("password not percent-encoded: %s", got)
	}
	if u.User.Username() != "a b" {
		t.Fatalf("username round-trip failed: %q", u.User.Username())
	}
}

func TestBuildProxyURL_IPv6Host(t *testing.T) {
	got, err := buildProxyURL(ProxyConfig{Enabled: true, Type: "HTTP", Host: "::1", Port: 1080})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "[::1]:1080") {
		t.Fatalf("IPv6 host not bracketed: %s", got)
	}
}

func TestBuildProxyURL_DisabledErrors(t *testing.T) {
	// disabled não valida, mas buildProxyURL chama Validate; disabled -> URL vazia válida?
	// Como Enabled=false, Validate retorna nil e scheme default "http" com host vazio => "http://:0".
	// Não chamamos buildProxyURL quando disabled, mas garantimos que não morre.
	_, err := buildProxyURL(ProxyConfig{Enabled: false})
	if err != nil {
		t.Fatalf("disabled should not error at build: %v", err)
	}
}

func TestProxyMasked_NeverExposesPassword(t *testing.T) {
	c := ProxyConfig{Enabled: true, Type: "SOCKS5", Host: "h", Port: 1, Username: "u", Password: "supersecret"}
	m := c.masked()
	if m.Password != "" {
		t.Fatalf("masked password must be empty, got %q", m.Password)
	}
	if !m.Set {
		t.Fatal("Set should be true when password is stored")
	}
	if m.Username != "u" || m.Host != "h" || m.Port != 1 || m.Type != "SOCKS5" || !m.Enabled {
		t.Fatalf("masked fields mismatch: %+v", m)
	}

	// Sem senha -> Set=false.
	m2 := ProxyConfig{Enabled: true, Type: "HTTP", Host: "h", Port: 1}.masked()
	if m2.Set {
		t.Fatal("Set should be false when no password stored")
	}
}
