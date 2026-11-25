package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"wacalls/internal/voip/core"
	"wacalls/internal/voip/media"
)

// pcmWSSubprotocol identifica o contrato de fio da ponte WebSocket: frames
// BINÁRIOS são PCM Int16 LE, mono, 16 kHz, nos dois sentidos (uplink e
// downlink usam o mesmo formato, sem Opus intermediário).
const pcmWSSubprotocol = "pcm16"

// wsPingInterval mantém a conexão viva através de proxies reversos (Cloudflare
// derruba conexões ociosas em ~100 s).
const wsPingInterval = 20 * time.Second

// wsBridge é a ponte de áudio por WebSocket da chamada: transporte alternativo
// ao pion/WebRTC para instalações atrás de proxy reverso HTTP (Cloudflare,
// Nginx, Traefik…) onde UDP é bloqueado ou o ICE não fecha.
//
// Garantias e limites (do transporte WS, não mais que isso):
//   - O transporte é TCP: os frames chegam em ordem e íntegros ENQUANTO o
//     socket vive. Não há retransmissão de aplicação, fila de envio nem
//     reconexão automática — se o socket fecha, a chamada é encerrada via
//     OnTerminalWS.
//   - WritePCM é bloqueante sob mutex: a própria TCP/wss aplica backpressure,
//     então um consumidor lento segura o callback de áudio em vez de acumular
//     memória. O preço é latência crescente enquanto o consumidor não drenar.
//   - Áudio-only (sem vídeo).
type wsBridge struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	// silenced: OnTerminalWS não deve disparar (hold/pickup/transferência, em
	// que a chamada não depende mais desta ponte). Diferente do AstraCalls, não
	// muta o áudio: a ponte continua entregando até setBridge substituí-la.
	silenced bool
	log      *slog.Logger

	// OnBrowserPCM recebe o uplink (navegador→WhatsApp) como PCM float32 16 kHz.
	OnBrowserPCM func(pcm []float32)
	// OnTerminalWS dispara quando o socket fecha sem Close/DisableTerminate.
	OnTerminalWS func()
}

func newWSBridge(conn *websocket.Conn, log *slog.Logger) *wsBridge {
	ctx, cancel := context.WithCancel(context.Background())
	return &wsBridge{conn: conn, ctx: ctx, cancel: cancel, log: log}
}

// WritePCM envia o downlink (WhatsApp→navegador) como PCM16 LE mono 16 kHz.
func (b *wsBridge) WritePCM(pcm []float32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(pcm) == 0 {
		return nil
	}
	return b.conn.Write(b.ctx, websocket.MessageBinary, media.PCMFloat32ToInt16LE(pcm))
}

// DisableTerminate evita que o fechamento dispare OnTerminalWS (hold/pickup/
// transferência) sem parar o áudio — a ponte só termina em Close().
func (b *wsBridge) DisableTerminate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.silenced = true
}

// Close encerra o WebSocket em definitivo.
func (b *wsBridge) Close() {
	b.mu.Lock()
	alreadyClosed := b.closed
	b.closed = true
	b.mu.Unlock()
	b.cancel()
	if !alreadyClosed {
		_ = b.conn.Close(websocket.StatusNormalClosure, "bridge closed")
	}
}

// readLoop lê frames PCM Int16 LE do navegador (uplink) e entrega ao
// CallManager. Bloqueia até o WebSocket fechar.
func (b *wsBridge) readLoop() {
	for {
		mt, data, err := b.conn.Read(b.ctx)
		if err != nil {
			break
		}
		if mt != websocket.MessageBinary || len(data) == 0 || b.OnBrowserPCM == nil {
			continue
		}
		b.OnBrowserPCM(media.PCMInt16LEToFloat32(data))
	}

	b.mu.Lock()
	fireCallback := !b.closed && !b.silenced
	b.closed = true
	b.mu.Unlock()

	if fireCallback && b.OnTerminalWS != nil {
		b.OnTerminalWS()
	}
}

// keepAlive envia pings periódicos para manter a conexão viva atrás de proxies.
func (b *wsBridge) keepAlive() {
	t := time.NewTicker(wsPingInterval)
	defer t.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
			err := b.conn.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// handleWSBridge atende GET /api/sessions/{sid}/calls/{id}/ws.
//
// A credencial vem de withAuth (header X-API-Key ou ?apiKey=, já que o browser
// não consegue enviar headers no handshake de WebSocket). Frames de texto não
// são usados: o ciclo de vida da chamada continua no SSE /api/events.
func (s *server) handleWSBridge(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	callID := r.PathValue("id")

	sess := s.sessionByID(w, sid)
	if sess == nil {
		return
	}
	ac, ok := sess.reg.get(callID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such call"})
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{pcmWSSubprotocol},
		// Origem não é validada aqui: a rota já está sob withAuth (API key) e o
		// painel pode ser servido de outra origem (withCORS libera CORS).
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.log.Warn("ws_bridge: upgrade failed", "sid", sid, "call", callID, "err", err)
		return
	}

	bridge := newWSBridge(conn, s.log)
	bridge.OnBrowserPCM = func(pcm []float32) {
		ac.cm.FeedCapturedPCM(pcm)
	}
	bridge.OnTerminalWS = func() {
		s.log.Info("ws_bridge: socket closed — ending call", "sid", sid, "call", callID)
		go sess.terminateCall(callID, core.EndCallReasonUserEnded)
	}

	// Instala a ponte: fecha a anterior (WebRTC ou outro WS) sem derrubar a
	// chamada — é assim que a troca de transporte do modo "auto" sobrevive.
	sess.setBridge(callID, bridge)

	s.log.Info("ws_bridge: connected", "sid", sid, "call", callID)

	go bridge.keepAlive()
	bridge.readLoop() // bloqueia até o socket fechar

	s.log.Info("ws_bridge: disconnected", "sid", sid, "call", callID)
}
