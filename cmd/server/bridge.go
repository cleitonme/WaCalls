package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
	"wacalls/internal/voip/media"
)

var (
	browserAPIOnce sync.Once
	browserAPI     *webrtc.API
)

// isPrivateIP retorna true se o IP for RFC1918 / loopback / link-local.
func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return true
	}
	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
	}
	for _, cidr := range private {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil && block.Contains(parsed) {
			return true
		}
	}
	return false
}

// detectPublicIPRoute tenta descobrir o IP pela rota padrão (sem pacotes).
// Dentro de Docker retorna o IP interno do container — use só como último recurso.
func detectPublicIPRoute() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// fetchPublicIP consulta serviços externos para obter o IP público real.
// Tenta múltiplos providers em ordem; retorna o primeiro sucesso.
func fetchPublicIP(log *slog.Logger) string {
	providers := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://checkip.amazonaws.com",
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, url := range providers {
		resp, err := client.Get(url)
		if err != nil {
			log.Debug("browser webrtc: public ip provider failed", "url", url, "err", err)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil && !isPrivateIP(ip) {
			log.Info("browser webrtc: resolved public IP via HTTP", "provider", url, "ip", ip)
			return ip
		}
	}
	return ""
}

// detectPublicIP resolve o IP público real:
// 1. Tenta rota padrão — se não for privado, usa direto (bare metal / VPS com IP na interface).
// 2. Senão faz HTTP para provider externo (caso Docker / NAT).
func detectPublicIP(log *slog.Logger) string {
	route := detectPublicIPRoute()
	if route != "" && !isPrivateIP(route) {
		log.Info("browser webrtc: auto-detected public IP via route", "ip", route)
		return route
	}
	if route != "" {
		log.Debug("browser webrtc: route IP is private, trying HTTP providers", "private_ip", route)
	}
	return fetchPublicIP(log)
}

func getBrowserAPI(log *slog.Logger) *webrtc.API {
	browserAPIOnce.Do(func() {
		publicIP := os.Getenv("WACALLS_PUBLIC_IP")
		udpPortStr := os.Getenv("WACALLS_UDP_PORT")
		if udpPortStr == "" {
			udpPortStr = "5000"
		}
		udpPort, _ := strconv.Atoi(udpPortStr)

		switch publicIP {
		case "auto", "":
			// "" também faz auto-detect para não quebrar quem não setou a env
			resolved := detectPublicIP(log)
			if resolved != "" {
				publicIP = resolved
			} else {
				log.Warn("browser webrtc: could not resolve public IP; using ephemeral (NAT may fail)")
				browserAPI = webrtc.NewAPI()
				return
			}
		}

		if udpPort == 0 {
			log.Warn("browser webrtc: invalid UDP port; using ephemeral")
			browserAPI = webrtc.NewAPI()
			return
		}

		se := webrtc.SettingEngine{}
		se.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
		se.SetNetworkTypes([]webrtc.NetworkType{
			webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6,
			webrtc.NetworkTypeTCP4, webrtc.NetworkTypeTCP6,
		})

		udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: udpPort})
		if err != nil {
			log.Error("browser webrtc: bind udp mux failed; falling back to ephemeral", "port", udpPort, "err", err)
			browserAPI = webrtc.NewAPI()
			return
		}
		se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udpConn))
		log.Info("browser webrtc: fixed udp port + nat1to1 enabled", "public_ip", publicIP, "udp_port", udpPort)

		if tcpListener, terr := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4zero, Port: udpPort}); terr == nil {
			se.SetICETCPMux(webrtc.NewICETCPMux(nil, tcpListener, 8))
			log.Info("browser webrtc: ice-tcp fallback enabled", "tcp_port", udpPort)
		} else {
			log.Error("browser webrtc: ice-tcp bind failed", "port", udpPort, "err", terr)
		}

		browserAPI = webrtc.NewAPI(webrtc.WithSettingEngine(se))
	})
	return browserAPI
}

// pcmChannelLabel is the data channel the browser opens to carry raw 16 kHz mono
// Int16 LE PCM in both directions. The browser side must create it with this label.
const pcmChannelLabel = "pcm"

// Bridge is the browser-leg adapter: it carries raw PCM between the browser and
// the CallManager over a WebRTC data channel.
type Bridge struct {
	pc  *webrtc.PeerConnection
	dc  atomic.Pointer[webrtc.DataChannel]
	log *slog.Logger

	OnBrowserPCM  func(pcm []float32)
	OnTerminalICE func()
}

func NewBridge(offerSDP string, log *slog.Logger) (*Bridge, string, error) {
	pc, err := getBrowserAPI(log).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, "", err
	}
	br := &Bridge{pc: pc, log: log}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != pcmChannelLabel {
			return
		}
		br.dc.Store(dc)
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if cb := br.OnBrowserPCM; cb != nil && len(msg.Data) > 0 {
				cb(media.PCMInt16LEToFloat32(msg.Data))
			}
		})
	})

	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		log.Debug("browser ice state", "state", s.String())
		if s == webrtc.ICEConnectionStateFailed || s == webrtc.ICEConnectionStateClosed {
			if br.OnTerminalICE != nil {
				br.OnTerminalICE()
			}
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		pc.Close()
		return nil, "", err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return nil, "", err
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		return nil, "", err
	}
	<-gatherComplete

	return br, pc.LocalDescription().SDP, nil
}

func (b *Bridge) WritePCM(pcm []float32) error {
	dc := b.dc.Load()
	if dc == nil || len(pcm) == 0 {
		return nil
	}
	return dc.Send(media.PCMFloat32ToInt16LE(pcm))
}

func (b *Bridge) Close() {
	if b.pc != nil {
		_ = b.pc.Close()
	}
}
