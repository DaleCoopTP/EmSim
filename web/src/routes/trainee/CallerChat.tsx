import { useState } from "react";
import { CloseIcon, MessageIcon } from "../../components/Arm112Icons";
import type { IntakeItem } from "./Operator112Workplace";

const maxLength = 500;

// 112-5a/ADR-024: a floating chat window over the card (like a site's
// chatbot widget), not embedded in the "Разговор с заявителем" section —
// so it can be collapsed while filling in the rest of the card without
// losing the conversation. open/onToggle are lifted to the parent so the
// arm112-line button and this window's own controls drive the same state.
export function CallerChat({ item, open, onToggle, pending, onSend }: {
  item: IntakeItem; open: boolean; onToggle: () => void; pending: boolean; onSend: (text: string) => void;
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
  const [text, setText] = useState("");
  // Adjusting state during render (react.dev's own pattern for "keep a
  // value in sync with a prop"), not an effect: readCount tracks
  // callerLines continuously while open — not just at the moment it
  // becomes open — so a reply that arrives while the window is already
  // open never counts as unread once it's collapsed again.
  const [readCount, setReadCount] = useState(callerLines);
  if (open && readCount !== callerLines) setReadCount(callerLines);
  const unread = open ? 0 : Math.max(0, callerLines - readCount);
  const trimmed = text.trim();
  const submit = () => {
    if (!canType || !trimmed || trimmed.length > maxLength) return;
    onSend(trimmed);
    setText("");
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
    {held && <p className="caller-chat-hint">Вызов на удержании. Вернитесь к разговору, чтобы написать заявителю.</p>}
    {!held && !connected && <p className="caller-chat-hint">Разговор завершён. История сохранена.</p>}
    {(connected || held) && <div className="caller-chat-input">
      <textarea aria-label="Сообщение заявителю" value={text} maxLength={maxLength} disabled={!canType}
        placeholder={waiting ? "Ожидание ответа…" : "Напишите сообщение…"}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); submit(); } }} />
      <div className="caller-chat-input-row">
        <span className="caller-chat-counter">{text.length} / {maxLength}</span>
        <button type="button" disabled={!canType || !trimmed} onClick={submit}>Отправить</button>
      </div>
    </div>}
  </div>;
}
