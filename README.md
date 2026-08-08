<div align="center">

# 📞 WaCalls (Go)

**Native WhatsApp voice calls in pure Go, straight from the browser.**
Built for native VoIP media, multi-account (multi-session) operation, and a modern browser client.

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](https://react.dev)
[![whatsmeow](https://img.shields.io/badge/whatsmeow-VoIP-25D366?logo=whatsapp&logoColor=white)](https://github.com/tulir/whatsmeow)
[![pion](https://img.shields.io/badge/pion-WebRTC-FF6B6B)](https://github.com/pion/webrtc)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](#license)

[Overview](#overview) · [Architecture](#architecture) · [Quick Start](#quick-start) · [API](#api) · [Security](#security)

</div>

---

## Overview

WaCalls pairs one or more WhatsApp accounts via **QR code** and lets you **place and
receive 1:1 voice calls** from any browser on the LAN. The browser microphone is sent
as **raw 16 kHz PCM over a WebRTC data channel** to the Go server, which encodes it with
Meta's **MLow** codec and injects the media into WhatsApp's **SRTP relay** mesh — and the
reverse path brings the peer's audio back to the browser.

The entire VoIP stack runs **natively in pure Go**: the MLow voice codec, **RTP/SRTP**
packetization, **STUN**, the **WebRTC/SCTP relay** transport and the `<call>` signaling,
integrated with [**whatsmeow**](https://github.com/tulir/whatsmeow) and served to a
**React 19** client. There is **no cgo and no native DLL** — the MLow codec is a vendored
pure-Go package, so a plain `go build` produces a self-contained binary with live audio.

Multiple WhatsApp accounts can be paired and operated side by side, each with its own
pairing QR, connection status, and history. A single account can also run **several
concurrent 1:1 calls** at once — one per browser operator — routed independently by call ID.

> **Status:** stable. Outgoing and incoming 1:1 calls reach `ACTIVE` with bidirectional
> audio, and a single account can hold several of them concurrently. Sessions persist in
> `wacalls.db` (pure-Go SQLite).

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          BROWSER (React client)                            │
│   mic + speaker  ·  WebRTC data channel (16 kHz PCM)  ·  HTTP + SSE         │
└───────────────────────────────┬──────────────────────────────────────────┘
                                 │  POST /api/sessions/{sid}/calls/{id}/webrtc  (SDP)
                                 │  GET  /api/events                            (SSE)
                                 ▼
┌──────────────────────────── GO SERVER (cmd/server) ────────────────────────┐
│  SessionManager   registry of accounts (client + CallManager + bridge)     │
│  Broker           SSE hub (sessions, auth, call lifecycle fan-out)          │
│  Bridge           pion WebRTC bridge (16 kHz PCM data channel ⇄ call core)  │
│                                                                            │
│  internal/wa      VoipSocket adapter over whatsmeow                        │
│  internal/voip    call · signaling · media · transport · core · wanode     │
└───────────────┬──────────────────────────────────────┬────────────────────┘
                │ <call> signaling (Signal/USync)       │ SRTP media
                ▼                                        ▼
        ┌───────────────┐                    ┌──────────────────────┐
        │  WhatsApp WS  │                    │   WhatsApp relay      │
        │  (whatsmeow)  │                    │  (SRTP over SCTP/DC)  │
        └───────────────┘                    └──────────────────────┘
```

### Layout

| Path | Responsibility |
|---|---|
| `cmd/server` | HTTP/SSE broker, session manager + store, WebRTC bridge, process lifecycle |
| `internal/wa` | `VoipSocket` — sends/receives `<call>` stanzas via whatsmeow |
| `internal/voip/core` | Domain types, constants, the `VoipSocket` interface |
| `internal/voip/wanode` | Shared WhatsApp-node and JID helpers |
| `internal/voip/media` | MLow codec (vendored pure-Go `mlow/`), RTP, SRTP, SSRC, PCM helpers, key derivation |
| `internal/voip/transport` | SCTP relay, STUN, subscription encoding |
| `internal/voip/signaling` | `<call>` stanza build/parse, call-key crypto, relay-ack parsing |
| `internal/voip/call` | `CallManager` — orchestrates a single call end to end |
| `client/` | React 19 + Vite + Tailwind v4 + shadcn/ui (dialer, call cards, sessions, history) |

---

## How a call flows

The core is `internal/voip/call.CallManager`, which drives a call end to end. Outgoing
call sequence:

```
1. POST .../calls            → CallManager.StartCall(peerJid)
                               generates a callID, builds the <call> offer, sends it

2. Browser opens WebRTC      → POST .../calls/{id}/webrtc (SDP offer)
                               the bridge answers with an SDP answer (pion)

3. Peer accepts              → events.CallAccept → HandleCallAccept
                               server receives <relay> + hop-by-hop keys

4. Relay transport           → STUN binding/allocate on WhatsApp relays
                               ICE + DTLS + SCTP DataChannel connect (pion)

5. SRTP media flowing        → state goes ACTIVE
   ├── uplink   (you → peer): browser 16 kHz PCM (data channel) → MLow encode → SRTP → relay
   └── downlink (peer → you): relay → SRTP → MLow decode → 16 kHz PCM (data channel) → browser

6. Teardown                  → DELETE .../calls/{id} or events.CallTerminate
                               CallManager.EndCall + bridge cleanup
```

Each protocol step (hop-by-hop SRTP key derivation, RTP packetization at `PT=120`/16 kHz,
STUN relay registration, relay-ack and `<call>` stanza parsing) is implemented and covered
by tests in `internal/voip` (`go test ./...`).

---

## Requirements

- **Go 1.26+**
- **Node 22+** and **npm** (only to build/run the React client)

No C compiler, cgo, or native libraries are required — the MLow codec is vendored
pure Go (`internal/voip/media/mlow`).

---

## Quick Start

```bash
# clone and enter the project
git clone <repo-url> wacalls-go
cd wacalls-go

# Go dependencies
go mod download

# React client dependencies
cd client && npm install && cd ..
```

### Run

```bash
go run ./cmd/server -addr :8080          # add -debug for verbose logs
```

Live audio works out of the box — the MLow codec is pure Go, so a plain build
includes it. No build tags, no `CGO_ENABLED`, no DLLs.

Open `http://localhost:8080`, click **New session**, and scan the QR shown in the browser
(it is also printed in the terminal) with **WhatsApp → Linked devices**. Add more accounts
the same way and switch between them in the sidebar.

### React client in dev mode

```bash
cd client
npm run dev      # Vite on :5173, proxies /api → http://localhost:8080
```

For production, build the static client and serve it from the Go server:

```bash
cd client && npm run build && cd ..
go run ./cmd/server -static client/dist -addr :8080
```

### Server flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8080` | HTTP listen address |
| `-db` | `wacalls.db` | SQLite session database path |
| `-static` | `client/dist` | Static client directory (optional) |
| `-debug` | `false` | Verbose logging (includes whatsmeow's internal log) |
| `-max-calls-per-session` | `8` | Max concurrent calls per session (`0` = unlimited) |

---

## API

All routes are session-scoped. Events stream over a single SSE channel, tagged with the
originating `sessionId`.

| Method | Route | Purpose |
|---|---|---|
| `GET` | `/api/sessions` | List accounts (id, name, jid, status, paired) |
| `POST` | `/api/sessions` | Create an account and begin QR pairing |
| `DELETE` | `/api/sessions/{sid}` | Log out and remove an account |
| `POST` | `/api/sessions/{sid}/logout` | Disconnect an account (keep it for re-pairing) |
| `POST` | `/api/sessions/{sid}/pair` | Re-pair an account (emit a fresh QR) |
| `POST` | `/api/sessions/{sid}/calls` | Start an outgoing call (`{ phone, duration_ms?, record? }`) |
| `POST` | `/api/sessions/{sid}/calls/{id}/webrtc` | Exchange the browser WebRTC SDP |
| `POST` | `/api/sessions/{sid}/calls/{id}/accept` | Accept an incoming call |
| `POST` | `/api/sessions/{sid}/calls/{id}/reject` | Reject an incoming call |
| `DELETE` | `/api/sessions/{sid}/calls/{id}` | End an active call |
| `GET` | `/api/sessions/{sid}/history` | Recent call history (up to 50 records) |
| `GET` | `/api/sessions/{sid}/proxy` | Get the channel's proxy config (password masked) |
| `POST` | `/api/sessions/{sid}/proxy` | Save proxy config and reconnect the channel |
| `POST` | `/api/sessions/{sid}/proxy/test` | Test proxy reachability without saving |
| `GET` | `/api/events` | Server-sent events (sessions, auth, call lifecycle) |

### Proxy

Each channel can route its WhatsApp connections (websocket + media) through an
**HTTP**, **HTTPS**, or **SOCKS5** proxy, independent per channel. The proxy is
applied when the whatsmeow client is created — so saving a new proxy disconnects
and reconnects the channel. **Hang up active calls before changing the proxy**
(the server returns `409` otherwise).

Persisted columns on the `sessions` table (added idempotently on first boot —
no manual migration needed):

| Column | Type | Notes |
|---|---|---|
| `proxy_enabled` | INTEGER | `0`/`1` |
| `proxy_type` | TEXT | `HTTP` / `HTTPS` / `SOCKS5` |
| `proxy_host` | TEXT | required when enabled |
| `proxy_port` | INTEGER | 1–65535 |
| `proxy_username` | TEXT | optional |
| `proxy_password` | TEXT | stored in plaintext (same as whatsmeow keys); **never returned** |

The password is **never** exposed: `GET …/proxy` and `SessionInfo.proxy` return
`proxyPassword` as `""` plus `proxySet` (`true` if a password is stored). Leave the
password blank on save to **preserve** the current one.

#### POST `/api/sessions/{sid}/proxy` — save

```json
{
  "proxyEnabled": true,
  "proxyType": "SOCKS5",
  "proxyHost": "proxy.meudominio.com",
  "proxyPort": 1080,
  "proxyUsername": "usuario",
  "proxyPassword": "senha"
}
```

Returns `{"ok": true}` and reconnects. Codes: `200`, `400` (invalid config),
`404` (no session), `409` (active calls), `500`.

#### POST `/api/sessions/{sid}/proxy/test` — test (no save)

Body: the same `ProxyConfig` above, plus optional `timeoutMs` (default 10000,
max 30000). An empty body tests the persisted config. The backend does a short
HTTPS `GET` through the proxy (SOCKS5 via `golang.org/x/net/proxy`) to fetch the
exit IP — it does **not** boot a whatsmeow client.

Response:

```json
{ "success": true, "latencyMs": 123, "exitIP": "203.0.113.5", "errorCategory": "" }
```

On failure `success` is `false` and `errorCategory` is one of: `timeout`,
`auth`, `host_invalid`, `scheme_invalid`, `connection_failed`, `http_error`,
`invalid`, `unknown`. Codes: `200` (always — the body carries success/failure),
`400` (invalid config), `404` (no session).

---

## Tests

```bash
go test ./...                 # media stack: SRTP, STUN, RTP, relay-ack, codec, state, proxy
cd client && npm run build    # client type-check + production build
```

Proxy probe tests hit live proxies and are gated behind a build tag + env so the
default suite never flakes offline:

```bash
WACALLS_PROXY_NETTEST=1 WACALLS_TEST_PROXY_HTTP=http://user:pass@host:port \
  go test -tags=proxynet ./cmd/server/ -run TestProbe -v
```

---

## Security

The API has **no authentication** — anyone with HTTP access can create accounts, place
calls, and read history. **Run it only on a trusted LAN.** `wacalls.db` holds WhatsApp
session credentials (secrets): **do not commit it** and keep it protected.

---

## Contributors

This project builds on the work of:

<div align="center">

<a href="https://github.com/jotadev66"><img src="https://github.com/jotadev66.png" width="72" height="72" style="border-radius:50%" alt="jotadev66"/></a>
<a href="https://github.com/jobasfernandes"><img src="https://github.com/jobasfernandes.png" width="72" height="72" style="border-radius:50%" alt="jobasfernandes"/></a>
<a href="https://github.com/edgardmessias"><img src="https://github.com/edgardmessias.png" width="72" height="72" style="border-radius:50%" alt="edgardmessias"/></a>
<a href="https://github.com/w3nder"><img src="https://github.com/w3nder.png" width="72" height="72" style="border-radius:50%" alt="w3nder"/></a>

[**@jotadev66**](https://github.com/jotadev66) · [**@jobasfernandes**](https://github.com/jobasfernandes) · [**@edgardmessias**](https://github.com/edgardmessias) · [**@w3nder**](https://github.com/w3nder)

</div>

---

## Acknowledgements

- [**whatsmeow**](https://github.com/tulir/whatsmeow) — Go WhatsApp Web protocol library
- [**pion/webrtc**](https://github.com/pion/webrtc) — pure-Go WebRTC stack (ICE + DTLS + SCTP)
- [**whatsapp-rust**](https://github.com/oxidezap/whatsapp-rust) — reference MLow codec implementation (ported to the vendored pure-Go `internal/voip/media/mlow`)
- [**zapo**](https://github.com/w3nder/zapo) — VoIP media-stack reference

---

## License

[MIT](./LICENSE)
