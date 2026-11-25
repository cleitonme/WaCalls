import { apiPost } from "./api";
import { float32ToInt16LE, int16LEToFloat32 } from "./pcm";
import {
  CAPTURE_PROCESSOR_NAME,
  CAPTURE_WORKLET_URL,
  PCM_CHANNEL_LABEL,
  PLAYBACK_PROCESSOR_NAME,
  PLAYBACK_WORKLET_URL,
  SAMPLE_RATE,
} from "../constants/audio";
import { getTransportMode } from "./transport";
import { openWSCall } from "./ws-audio";

export type OpenCall = {
  pc: RTCPeerConnection | null;
  micStream: MediaStream;
  remoteStream: MediaStream | null;
  close: () => void;
};

// =============================================================================
// Transporte adaptativo — escolhe WebRTC ou WebSocket conforme transport.ts
// =============================================================================

// Tempo (ms) que esperamos o ICE do WebRTC conectar antes de cair para o
// WebSocket no modo "auto". Curto o suficiente para não deixar o usuário no
// vácuo e folgado o suficiente para redes lentas fecharem o ICE.
const ICE_FALLBACK_MS = 6000;

// waitIceConnected resolve true quando o PeerConnection conecta o ICE, e false
// se falhar/fechar ou estourar o timeout (aí o chamador cai para o WebSocket).
const waitIceConnected = (pc: RTCPeerConnection, timeoutMs: number): Promise<boolean> =>
  new Promise((resolve) => {
    let settled = false;
    const finish = (v: boolean) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      pc.removeEventListener("iceconnectionstatechange", onChange);
      resolve(v);
    };
    const onChange = () => {
      const s = pc.iceConnectionState;
      if (s === "connected" || s === "completed") finish(true);
      else if (s === "failed" || s === "closed") finish(false);
      // "disconnected"/"checking": transitório — aguarda o timeout
    };
    const timer = setTimeout(() => finish(false), timeoutMs);
    pc.addEventListener("iceconnectionstatechange", onChange);
    onChange(); // caso já tenha conectado antes do listener
  });

// wsAdapter embrulha openWSCall no formato OpenCall.
const wsAdapter = async (
  sid: string,
  callId: string,
  micDeviceId: string | null,
): Promise<OpenCall> => {
  const conn = await openWSCall(sid, callId, micDeviceId);
  return {
    pc: null, // WS não usa RTCPeerConnection
    micStream: conn.micStream,
    remoteStream: conn.remoteStream,
    close: conn.close,
  };
};

/**
 * openAdaptiveCall — ponto de entrada das chamadas. Consulta getTransportMode():
 *  - "webrtc"    (forçado): só WebRTC, sem fallback.
 *  - "websocket" (forçado): só WebSocket (áudio-only, wss/443) — prioriza
 *    entrega confiável de áudio onde UDP não fecha.
 *  - "auto"      (padrão): tenta WebRTC; se o ICE não conectar em
 *    ICE_FALLBACK_MS, fecha e cai para o WebSocket.
 *
 * Trocar o transporte não derruba a chamada: ao instalar a nova ponte o
 * servidor desliga o callback terminal da anterior (setBridge).
 */
export const openAdaptiveCall = async (
  sid: string,
  callId: string,
  micDeviceId: string | null,
): Promise<OpenCall> => {
  const mode = getTransportMode();

  if (mode === "websocket") {
    return wsAdapter(sid, callId, micDeviceId);
  }
  if (mode === "webrtc") {
    return openCall(sid, callId, micDeviceId);
  }

  // mode === "auto": tenta WebRTC e cai para o WebSocket se o ICE não conectar.
  try {
    const call = await openCall(sid, callId, micDeviceId);
    if (call.pc && (await waitIceConnected(call.pc, ICE_FALLBACK_MS))) {
      return call;
    }
    console.warn(
      `[transport] WebRTC não conectou (ICE) em ${ICE_FALLBACK_MS}ms — caindo para WebSocket (áudio-only)`,
    );
    try {
      call.close();
    } catch {
      /* ignore */
    }
  } catch (err) {
    console.warn("[transport] WebRTC falhou ao abrir — caindo para WebSocket (áudio-only)", err);
  }
  return wsAdapter(sid, callId, micDeviceId);
};

export const openCall = async (
  sid: string,
  callId: string,
  micDeviceId: string | null,
): Promise<OpenCall> => {
  const micStream = await navigator.mediaDevices.getUserMedia({
    audio: micDeviceId ? { deviceId: { exact: micDeviceId } } : true,
  });

  const pc = new RTCPeerConnection({ iceServers: [] });

  const dc = pc.createDataChannel(PCM_CHANNEL_LABEL, { ordered: true });
  dc.binaryType = "arraybuffer";

  const ctx = new AudioContext({ sampleRate: SAMPLE_RATE });
  await ctx.audioWorklet.addModule(CAPTURE_WORKLET_URL);
  await ctx.audioWorklet.addModule(PLAYBACK_WORKLET_URL);
  await ctx.resume();

  const micSource = ctx.createMediaStreamSource(micStream);
  const captureNode = new AudioWorkletNode(ctx, CAPTURE_PROCESSOR_NAME);
  captureNode.port.onmessage = (e: MessageEvent<Float32Array>) => {
    if (dc.readyState === "open") dc.send(float32ToInt16LE(e.data));
  };
  micSource.connect(captureNode);
  captureNode.connect(ctx.destination);

  const playbackNode = new AudioWorkletNode(ctx, PLAYBACK_PROCESSOR_NAME);
  const streamDest = ctx.createMediaStreamDestination();
  playbackNode.connect(streamDest);
  dc.onmessage = (e: MessageEvent<ArrayBuffer>) => {
    playbackNode.port.postMessage(int16LEToFloat32(e.data));
  };

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  await new Promise<void>((resolve) => {
    if (pc.iceGatheringState === "complete") resolve();
    else
      pc.addEventListener("icegatheringstatechange", () => {
        if (pc.iceGatheringState === "complete") resolve();
      });
  });

  const { sdp_answer } = await apiPost<{ sdp_answer: string }>(
    `/api/sessions/${sid}/calls/${callId}/webrtc`,
    { sdp_offer: pc.localDescription!.sdp },
  );
  await pc.setRemoteDescription({ type: "answer", sdp: sdp_answer });

  return {
    pc,
    micStream,
    remoteStream: streamDest.stream,
    close: () => {
      try {
        micStream.getTracks().forEach((t) => t.stop());
      } catch {}
      try {
        ctx.close();
      } catch {}
      try {
        pc.close();
      } catch {}
    },
  };
};
