// 112-8a/ADR-037: records one spoken phrase from the microphone and hands
// it back as a 16 kHz mono WAV (see wav.ts). Audio lives only in memory and
// is sent to POST /items/{id}/dictation; nothing is kept in the browser.
import { downsample, encodeWav } from "./wav";

export type MicrophoneFailure = "insecure" | "unsupported" | "denied" | "unavailable";

export class MicrophoneError extends Error {
  readonly kind: MicrophoneFailure;
  constructor(kind: MicrophoneFailure) {
    super(kind);
    this.kind = kind;
  }
}

export type Recorder = {
  // stop ends the recording and resolves the phrase; it never rejects.
  stop: () => Promise<Blob>;
  // cancel throws the phrase away (window collapsed, component gone).
  cancel: () => void;
};

const workletSource = `class EmsimCapture extends AudioWorkletProcessor {
  process(inputs) {
    const channel = inputs[0] && inputs[0][0];
    if (channel) this.port.postMessage(channel.slice(0));
    return true;
  }
}
registerProcessor("emsim-capture", EmsimCapture);`;

// startRecorder asks for the microphone and starts capturing. onLimit is
// called once when maxSeconds of audio have been captured; the caller then
// calls stop(). Anything past the limit is dropped.
export async function startRecorder(maxSeconds: number, onLimit: () => void): Promise<Recorder> {
  if (!window.isSecureContext) throw new MicrophoneError("insecure");
  if (!navigator.mediaDevices?.getUserMedia || typeof AudioContext === "undefined" || typeof AudioWorkletNode === "undefined") {
    throw new MicrophoneError("unsupported");
  }
  let stream: MediaStream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
  } catch (cause) {
    const name = cause instanceof DOMException ? cause.name : "";
    throw new MicrophoneError(name === "NotAllowedError" || name === "SecurityError" ? "denied" : "unavailable");
  }
  const context = new AudioContext();
  const release = () => { stream.getTracks().forEach((track) => track.stop()); void context.close().catch(() => undefined); };
  const chunks: Float32Array[] = [];
  let total = 0;
  let limited = false;
  let node: AudioWorkletNode;
  try {
    const moduleUrl = URL.createObjectURL(new Blob([workletSource], { type: "application/javascript" }));
    try { await context.audioWorklet.addModule(moduleUrl); } finally { URL.revokeObjectURL(moduleUrl); }
    node = new AudioWorkletNode(context, "emsim-capture");
  } catch {
    release();
    throw new MicrophoneError("unsupported");
  }
  const limit = Math.floor(maxSeconds * context.sampleRate);
  node.port.onmessage = (event: MessageEvent<Float32Array>) => {
    if (total >= limit) {
      if (!limited) { limited = true; onLimit(); }
      return;
    }
    chunks.push(event.data);
    total += event.data.length;
  };
  const source = context.createMediaStreamSource(stream);
  // A worklet with no output connection may not be pulled by the browser;
  // route it through a muted gain so nothing is played back.
  const mute = context.createGain();
  mute.gain.value = 0;
  source.connect(node).connect(mute).connect(context.destination);
  const sampleRate = context.sampleRate;
  let finished = false;
  const finish = () => { if (!finished) { finished = true; node.port.onmessage = null; release(); } };
  return {
    stop: async () => {
      finish();
      const merged = new Float32Array(total);
      let offset = 0;
      for (const chunk of chunks) { merged.set(chunk.subarray(0, Math.min(chunk.length, total - offset)), offset); offset += chunk.length; }
      return encodeWav(downsample(merged, sampleRate));
    },
    cancel: finish,
  };
}
