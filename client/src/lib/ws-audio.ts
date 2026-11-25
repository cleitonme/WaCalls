/**
 * ws-audio.ts — transporte de mídia por WebSocket (wss/443).
 *
 * Alternativa ao RTCPeerConnection quando o WebRTC/UDP não fecha (proxy reverso,
 * firewall bloqueando UDP). Atravessa qualquer proxy HTTP e é a opção que
 * prioriza entrega de áudio confiável no transport.ts.
 *
 * Protocolo (contrato "pcm16" com o servidor Go):
 *   - Uplink   (mic → servidor): frames binários PCM Int16 LE, 16 kHz, mono
 *   - Downlink (servidor → speaker): mesma codificação
 *   - Frames de texto não são usados; o ciclo de vida vem pelo SSE /api/events.
 *
 * Limites: é áudio-only e o transporte é TCP — entrega ordenada e íntegra
 * ENQUANTO o socket vive. Não há reconexão automática nem fila de envio: se o
 * socket cair, o servidor encerra a chamada, e frames capturados fora de OPEN
 * são descartados (com um aviso único no console). O downlink passa pelo
 * playback-processor, que mantém o buffer de jitter — mesma arquitetura do
 * transporte WebRTC.
 */

import { getApiBase, getApiKey } from "./auth";
import { float32ToInt16LE, int16LEToFloat32 } from "./pcm";
import {
  CAPTURE_PROCESSOR_NAME,
  CAPTURE_WORKLET_URL,
  PLAYBACK_PROCESSOR_NAME,
  PLAYBACK_WORKLET_URL,
  SAMPLE_RATE,
} from "../constants/audio";

const PCM_SUBPROTOCOL = "pcm16";

export type WSAudioState = "connecting" | "connected" | "disconnected";

export type WSAudioEvents = {
  onState?: (state: WSAudioState) => void;
  onError?: (err: Error) => void;
};

const wsBaseUrl = (): string => {
  const base = getApiBase();
  if (base) return base.replace(/^https:\/\//i, "wss://").replace(/^http:\/\//i, "ws://");
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}`;
};

export class WSAudioBridge {
  /** Stream de microfone — disponível após connect() para o VU meter */
  micStream: MediaStream | null = null;

  /** Stream de áudio remoto — disponível após connect() para o VU meter */
  remoteStream: MediaStream | null = null;

  private ws: WebSocket | null = null;
  private ctx: AudioContext | null = null;
  private micSource: MediaStreamAudioSourceNode | null = null;
  private captureNode: AudioWorkletNode | null = null;
  private playbackNode: AudioWorkletNode | null = null;
  private streamDest: MediaStreamAudioDestinationNode | null = null;
  private droppedFrames = 0;
  private events: WSAudioEvents;
  private micDeviceId: string | null;

  constructor(
    private sid: string,
    private callId: string,
    micDeviceId: string | null = null,
    events: WSAudioEvents = {},
  ) {
    this.micDeviceId = micDeviceId;
    this.events = events;
  }

  async connect(): Promise<void> {
    this.events.onState?.("connecting");

    const micStream = await navigator.mediaDevices.getUserMedia({
      audio: this.micDeviceId ? { deviceId: { exact: this.micDeviceId } } : true,
    });
    this.micStream = micStream;

    const apiKey = getApiKey();
    const url =
      `${wsBaseUrl()}/api/sessions/${this.sid}/calls/${this.callId}/ws` +
      (apiKey ? `?apiKey=${encodeURIComponent(apiKey)}` : "");

    try {
      await this.openSocket(url);
      await this.startAudio();
    } catch (err) {
      this.disconnect();
      throw err;
    }
  }

  private openSocket(url: string): Promise<void> {
    const ws = new WebSocket(url, [PCM_SUBPROTOCOL]);
    ws.binaryType = "arraybuffer";
    this.ws = ws;

    return new Promise<void>((resolve, reject) => {
      let settled = false;
      ws.onopen = () => {
        if (settled) return;
        settled = true;
        // Zera o contador: se o socket cair depois, o aviso volta a aparecer.
        this.droppedFrames = 0;
        this.events.onState?.("connected");
        resolve();
      };
      ws.onerror = () => {
        if (settled) return;
        settled = true;
        const err = new Error("WebSocket connection failed");
        this.events.onError?.(err);
        reject(err);
      };
      ws.onclose = () => {
        this.events.onState?.("disconnected");
        if (!settled) {
          settled = true;
          reject(new Error("WebSocket closed before opening"));
        }
        this.stopCapture();
      };
      ws.onmessage = (ev) => {
        if (ev.data instanceof ArrayBuffer) this.playPCM(ev.data);
      };
    });
  }

  private async startAudio(): Promise<void> {
    if (!this.micStream) return;

    const ctx = new AudioContext({ sampleRate: SAMPLE_RATE });
    await ctx.audioWorklet.addModule(CAPTURE_WORKLET_URL);
    await ctx.audioWorklet.addModule(PLAYBACK_WORKLET_URL);
    await ctx.resume();
    this.ctx = ctx;

    const micSource = ctx.createMediaStreamSource(this.micStream);
    this.micSource = micSource;

    const captureNode = new AudioWorkletNode(ctx, CAPTURE_PROCESSOR_NAME);
    this.captureNode = captureNode;
    captureNode.port.onmessage = (e: MessageEvent<Float32Array>) => {
      const ws = this.ws;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        // Sem fila: o áudio capturado durante a queda não é reenviado. Avisa
        // uma vez para o áudio não sumir silenciosamente.
        this.droppedFrames += 1;
        if (this.droppedFrames === 1) {
          console.warn(
            "[ws-audio] socket not OPEN — microphone frames dropped (no retry queue)",
          );
        }
        return;
      }
      ws.send(float32ToInt16LE(e.data));
    };
    micSource.connect(captureNode);
    captureNode.connect(ctx.destination);

    const playbackNode = new AudioWorkletNode(ctx, PLAYBACK_PROCESSOR_NAME);
    this.playbackNode = playbackNode;
    const streamDest = ctx.createMediaStreamDestination();
    this.streamDest = streamDest;
    playbackNode.connect(streamDest);
    this.remoteStream = streamDest.stream;
  }

  /** Entrega o downlink ao playback-processor (que mantém o jitter buffer). */
  private playPCM(buf: ArrayBuffer): void {
    this.playbackNode?.port.postMessage(int16LEToFloat32(buf));
  }

  /** Silencia/retoma o microfone (hold). */
  setMicEnabled(enabled: boolean): void {
    this.micStream?.getAudioTracks().forEach((t) => {
      t.enabled = enabled;
    });
  }

  stopCapture(): void {
    this.captureNode?.disconnect();
    this.micSource?.disconnect();
    this.playbackNode?.disconnect();
    this.streamDest?.disconnect();
    this.ctx?.close().catch(() => {});
    this.ctx = null;
    this.captureNode = null;
    this.micSource = null;
    this.playbackNode = null;
    this.streamDest = null;
    this.remoteStream = null;
    this.micStream?.getTracks().forEach((t) => t.stop());
    this.micStream = null;
  }

  disconnect(): void {
    this.stopCapture();
    const ws = this.ws;
    this.ws = null;
    if (!ws) return;
    if (ws.readyState === WebSocket.OPEN) {
      ws.close(1000, "call ended");
    } else if (ws.readyState === WebSocket.CONNECTING) {
      ws.onopen = null;
      ws.onerror = null;
      ws.onclose = null;
      ws.close();
    }
  }
}

export type OpenWSCall = {
  pc: null;
  micStream: MediaStream;
  remoteStream: MediaStream | null;
  close: () => void;
};

/** openWSCall — interface equivalente a openCall() de webrtc.ts. */
export const openWSCall = async (
  sid: string,
  callId: string,
  micDeviceId: string | null,
): Promise<OpenWSCall> => {
  const bridge = new WSAudioBridge(sid, callId, micDeviceId);
  await bridge.connect();

  return {
    pc: null,
    get micStream() {
      return bridge.micStream as MediaStream;
    },
    get remoteStream() {
      return bridge.remoteStream;
    },
    close: () => bridge.disconnect(),
  };
};
