// SSE invalidation client (RFC-001 §7.7/ADR-018): every stream carries
// nothing but "something changed" — the caller's onInvalidate always
// re-reads state from the REST API, exactly as a `stream.ready` or
// `resync` event on the wire means "read a full snapshot now" and a
// plain `invalidate` means "something in scope changed, read again".
// The browser's own EventSource reconnect (Last-Event-ID, since every
// event the server sends carries an `id:`) handles a dropped connection
// without this module's help; onInvalidate is called again once it
// reconnects and the server replies with its own stream.ready/resync/
// invalidate, so callers do not need special reconnect handling either.
import { useEffect, useRef } from "react";

export function useEventStream(path: string, onInvalidate: () => void): void {
  const callback = useRef(onInvalidate);
  useEffect(() => {
    callback.current = onInvalidate;
  });

  useEffect(() => {
    const source = new EventSource(`/api/v1${path}`, { withCredentials: true });
    const invalidate = () => callback.current();
    source.addEventListener("stream.ready", invalidate);
    source.addEventListener("resync", invalidate);
    source.addEventListener("invalidate", invalidate);
    // A transport-level error (including one EventSource will retry on
    // its own) is not itself actionable here — the next stream.ready/
    // resync after it reconnects is what triggers a re-read.
    return () => source.close();
  }, [path]);
}
