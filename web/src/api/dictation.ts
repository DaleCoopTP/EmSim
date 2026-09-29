import { api } from "./client";

// 112-8a/ADR-037: POST /items/{id}/dictation — one WAV phrase in, text out.
// Nothing is stored server-side; the text goes into the chat input and is
// sent as an ordinary send_caller_message with input=voice.
export type DictationResult = { text: string; model: string; duration_ms: number };

export const dictate = (itemId: string, wav: Blob) => api.postBlob<DictationResult>(`/items/${itemId}/dictation`, wav);
