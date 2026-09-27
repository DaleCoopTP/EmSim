import { useState } from "react";
import { CloseIcon, MessageIcon } from "../../components/Arm112Icons";
import type { IntakeItem } from "./Operator112Workplace";

const maxLength = 500;

// 112-5a/ADR-024: a floating chat window over the card (like a site's
// chatbot widget), not embedded in the "Разговор с заявителем" section —
// so it can be collapsed while filling in the rest of the card without
// losing the conversation. open/onToggle are lifted to the parent so the
// arm112-line button and this window's own controls drive the same state.
// Characters are counted as Unicode code points, the same way the
// server (utf8.RuneCountInString) and OpenAPI's maxLength do — not
// String.length's UTF-16 units, which count an emoji as two.
const charCount = (value: string) => [...value].length;

export function CallerChat({ item, open, onToggle, pending, rejected, onSend }: {
  item: IntakeItem; open: boolean; onToggle: () => void; pending: boolean; rejected: string | null; onSend: (text: string) => void;
}) {
  const state = item.intake_state;
  const terminal = item.state === "closed" || item.state === "interrupted";
  const turns = state.caller_turns ?? [];
  const lastTurn = turns[turns.length - 1];
  const waiting = lastTurn?.status === "pending";
  const failed = lastTurn?.status === "failed";
  const held = state.call_status === "held";
  const connected = state.call_status === "connected";
  const canType = connected && !terminal && !waiting && !pending;
  const callerLines = state.transcript.filter((line) => line.speaker === "caller").length;
  const operatorLines = state.transcript.filter((line) => line.speaker === "operator").length;
  const [text, setText] = useState("");
  // The typed text is cleared only once the message actually lands in
  // the transcript (a new operator line), not at submit time — a
  // rejected send_caller_message leaves it in place to fix and resend
  // (review 2026-09-26, item 9). Same adjust-during-render pattern as
  // readCount below.
  const [sentAt, setSentAt] = useState<number | null>(null);
  if (sentAt !== null && operatorLines > sentAt) {
    setSentAt(null);
    setText("");
  }
  // Adjusting state during render (react.dev's own pattern for "keep a
  // value in sync with a prop"), not an effect: readCount tracks
  // callerLines continuously while open — not just at the moment it
  // becomes open — so a reply that arrives while the window is already
  // open never counts as unread once it's collapsed again.
  const [readCount, setReadCount] = useState(callerLines);
  if (open && readCount !== callerLines) setReadCount(callerLines);
  const unread = open ? 0 : Math.max(0, callerLines - readCount);
  const trimmed = text.trim();
  const tooLong = charCount(trimmed) > maxLength;
  const submit = () => {
    if (!canType || !trimmed || tooLong) return;
    setSentAt(operatorLines);
    onSend(trimmed);
  };

  if (!open) return <button type="button" className="caller-chat-launcher" aria-label="Открыть чат с заявителем" onClick={onToggle}>
    <MessageIcon size={22} />
    {unread > 0 && <span className="caller-chat-badge">{unread}</span>}
  </button>;

  return <div className="caller-chat" role="dialog" aria-label="Чат с заявителем">
    <header className="caller-chat-head">
      <div className="caller-chat-head-title"><strong>Заявитель</strong><span>{item.card.aon}</span></div>
      <span className="caller-chat-head-status">
        {state.call_status === "connected" ? "разговор" : state.call_status === "held" ? "на удержании" : state.call_status === "ended" ? "завершён" : ""}
      </span>
      <button type="button" className="caller-chat-collapse" aria-label="Свернуть чат" onClick={onToggle}><CloseIcon size={14} /></button>
    </header>
    <div className="caller-chat-log" aria-live="polite">
      {state.transcript.length === 0 && <p className="caller-chat-hint">Напишите первое сообщение заявителю.</p>}
      {state.transcript.map((line, index) => <p key={line.id ?? index} className={`caller-chat-line caller-chat-${line.speaker ?? "operator"}`}>{line.text}</p>)}
      {waiting && <p className="caller-chat-typing">Заявитель печатает…</p>}
      {failed && <p className="caller-chat-line caller-chat-system">Заявитель не ответил (техническая причина) — повторите сообщение.</p>}
    </div>
    {rejected && !pending && <p className="caller-chat-line caller-chat-system" role="alert">Сообщение не отправлено: {rejected}. Текст сохранён — исправьте его и отправьте снова.</p>}
    {held && <p className="caller-chat-hint">Вызов на удержании. Вернитесь к разговору, чтобы написать заявителю.</p>}
    {!held && !connected && <p className="caller-chat-hint">Разговор завершён. История сохранена.</p>}
    {(connected || held) && <div className="caller-chat-input">
      <textarea aria-label="Сообщение заявителю" value={text} disabled={!canType}
        placeholder={waiting ? "Ожидание ответа…" : "Напишите сообщение…"}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); submit(); } }} />
      <div className="caller-chat-input-row">
        <span className={tooLong ? "caller-chat-counter error" : "caller-chat-counter"}>{charCount(text)} / {maxLength}</span>
        <button type="button" disabled={!canType || !trimmed || tooLong} onClick={submit}>Отправить</button>
      </div>
    </div>}
  </div>;
}
