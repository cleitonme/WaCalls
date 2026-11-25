/**
 * transport.ts — seleção do transporte de mídia da chamada.
 *
 *   - "webrtc":    áudio PCM16 sobre data channel do RTCPeerConnection (padrão
 *                  histórico; mantém vídeo quando existir).
 *   - "websocket": áudio via WSS/443 (PCM16). Atravessa proxy reverso HTTP
 *                  (Cloudflare, Nginx, firewall que bloqueia UDP). Sem vídeo.
 *
 * Seleção (ordem de prioridade):
 *   1. Query param na URL:  ?transport=ws   (ou ?transport=webrtc)
 *   2. localStorage:        wacalls.transport = "websocket" | "webrtc"
 *   3. padrão injetado pelo servidor no index.html
 *      (window.__WACALLS_DEFAULT_TRANSPORT, vindo de WACALLS_DEFAULT_TRANSPORT)
 *   4. "auto"
 *
 * No modo "auto" o openAdaptiveCall tenta WebRTC e, se o ICE NÃO conectar em
 * alguns segundos (proxy/firewall bloqueando UDP), cai automaticamente para o
 * WebSocket (áudio-only) — sem o operador abrir porta nem escolher nada.
 * Forçar ?transport= desliga o fallback.
 */
export type Transport = "webrtc" | "websocket";

/**
 * Modo de transporte, distinguindo escolha EXPLÍCITA de "auto" (nada escolhido).
 *   - "webrtc" / "websocket": operador forçou via ?transport=, localStorage ou
 *     WACALLS_DEFAULT_TRANSPORT no servidor.
 *   - "auto" (padrão): sem escolha — o openAdaptiveCall tenta WebRTC e, se o
 *     ICE não conectar, cai automaticamente para WebSocket (áudio-only).
 */
export type TransportMode = Transport | "auto";

const STORAGE_KEY = "wacalls.transport";

function normalize(v: string | null): Transport | null {
  if (!v) return null;
  const s = v.trim().toLowerCase();
  if (s === "ws" || s === "wss" || s === "websocket") return "websocket";
  if (s === "rtc" || s === "webrtc") return "webrtc";
  return null;
}

export function getTransportMode(): TransportMode {
  try {
    const fromQuery = normalize(new URLSearchParams(window.location.search).get("transport"));
    if (fromQuery) return fromQuery;
  } catch {
    /* ambiente sem window.location — ignora */
  }
  try {
    const fromStore = normalize(localStorage.getItem(STORAGE_KEY));
    if (fromStore) return fromStore;
  } catch {
    /* localStorage indisponível — ignora */
  }
  // Padrão do servidor (injetado no index.html via WACALLS_DEFAULT_TRANSPORT).
  // Permite fixar o transporte por instância (ex.: "websocket" onde o WebRTC/UDP
  // não fecha) sem o agente precisar de ?transport=. Query e localStorage têm
  // prioridade sobre isso.
  try {
    const fromServer = normalize(
      (window as unknown as { __WACALLS_DEFAULT_TRANSPORT?: string }).__WACALLS_DEFAULT_TRANSPORT ??
        null,
    );
    if (fromServer) return fromServer;
  } catch {
    /* ignora */
  }
  return "auto";
}

/** Retorna o transporte efetivo (modo "auto" resolve para "webrtc"). */
export function getTransport(): Transport {
  return getTransportMode() === "websocket" ? "websocket" : "webrtc";
}
