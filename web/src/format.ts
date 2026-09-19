// Shared display formatting — nothing here is React-specific, so both
// route components and presentational components (IncidentCard) can use
// it without a component-to-component import.

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString("ru-RU", { dateStyle: "short", timeStyle: "short" });
}

// card.registered_at_offset_s (CardPreview) is seconds relative to the
// card's offer time, usually negative ("registered before offered") —
// there is no absolute registered_at until a real run exists (slice 3).
export function formatOffset(offsetSeconds: number): string {
  if (offsetSeconds < 0) return `За ${-offsetSeconds} с до выдачи`;
  if (offsetSeconds === 0) return "В момент выдачи";
  return `Через ${offsetSeconds} с после выдачи`;
}
