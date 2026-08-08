package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ProxyConfig é a configuração de proxy de um canal, lida da API e persistida no
// banco. A senha fica em texto simples (mesmo padrão das chaves do WhatsApp no
// whatsmeow.db). NUNCA serializar ProxyConfig diretamente em logs/responses que
// possam vazar a senha — use ProxyInfo (mascarado) para expor estado.
type ProxyConfig struct {
	Enabled  bool   `json:"proxyEnabled"`
	Type    string `json:"proxyType"`    // "HTTP" | "HTTPS" | "SOCKS5"
	Host    string `json:"proxyHost"`
	Port    int    `json:"proxyPort"`
	Username string `json:"proxyUsername"`
	Password string `json:"proxyPassword"` // em claro no DB; não exposto em ProxyInfo
}

// ProxyInfo é a forma mascarada exposta em SessionInfo/SSE: a senha é sempre "".
// "Set" indica se há senha armazenada, sem revelá-la (o frontend não a ecoa de
// volta no formulário — deixar senha em branco ao salvar preserva a atual).
type ProxyInfo struct {
	Enabled  bool   `json:"proxyEnabled"`
	Type    string `json:"proxyType"`
	Host    string `json:"proxyHost"`
	Port    int    `json:"proxyPort"`
	Username string `json:"proxyUsername"`
	Password string `json:"proxyPassword"` // sempre ""
	Set     bool   `json:"proxySet"`       // true se há senha armazenada
}

// scheme normaliza o tipo de proxy para o scheme de URL que o whatsmeow aceita.
// "HTTP" ou qualquer valor não reconhecido cai em "http" (SetProxyAddress aceita
// http/https/socks5); chamamos Validate antes, então os únicos valores que chegam
// aqui são os três válidos.
func (c ProxyConfig) scheme() string {
	switch strings.ToUpper(strings.TrimSpace(c.Type)) {
	case "HTTPS":
		return "https"
	case "SOCKS5":
		return "socks5"
	default:
		return "http"
	}
}

// Validate verifica a configuração de proxy. Proxy desabilitado é sempre válido
// (nenhum campo requerido); habilitado exige tipo válido, host não-vazio e porta
// 1–65535.
func (c ProxyConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	t := strings.ToUpper(strings.TrimSpace(c.Type))
	if t != "HTTP" && t != "HTTPS" && t != "SOCKS5" {
		return fmt.Errorf("proxyType deve ser HTTP, HTTPS ou SOCKS5")
	}
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("proxyHost é obrigatório quando o proxy está ativo")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("proxyPort deve estar entre 1 e 65535")
	}
	return nil
}

// buildProxyURL monta a URL do proxy no formato esperado por whatsmeow.SetProxyAddress:
// scheme://[user:pass@]host[:port]. user/senha são percent-encodados por url.UserPassword
// (RFC 3986) e hosts IPv6 são delimitados por colchetes via net.JoinHostPort.
func buildProxyURL(c ProxyConfig) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	u := &url.URL{
		Scheme: c.scheme(),
		Host:   net.JoinHostPort(strings.TrimSpace(c.Host), strconv.Itoa(c.Port)),
	}
	if strings.TrimSpace(c.Username) != "" {
		u.User = url.UserPassword(c.Username, c.Password)
	}
	return u.String(), nil
}

// masked devolve a ProxyInfo correspondente, com a senha sempre vazia e Set
// refleterindo se há senha persistida. NUNCA inclui a senha real.
func (c ProxyConfig) masked() ProxyInfo {
	return ProxyInfo{
		Enabled:  c.Enabled,
		Type:    c.Type,
		Host:    c.Host,
		Port:    c.Port,
		Username: c.Username,
		Password: "",
		Set:     c.Password != "",
	}
}
