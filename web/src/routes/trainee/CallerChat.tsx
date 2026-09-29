import { useEffect, useRef, useState } from "react";
import { ApiError } from "../../api/client";
import { dictate } from "../../api/dictation";
import { CloseIcon, MessageIcon, MicIcon } from "../../components/Arm112Icons";
import { MicrophoneError, startRecorder, type Recorder } from "../../lib/microphone";
import type { IntakeItem } from "./Operator112Workplace";
import "../../dictation.css";

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

// 112-8a/ADR-037: what the operator sees when dictation fails. Typing keeps
// working in every case, and the text already in the box is never touched.
function dictationErrorText(cause: unknown): string {
  if (cause instanceof MicrophoneError) {
    switch (cause.kind) {
      case "insecure": return "Микрофон доступен только по защищённому соединению (HTTPS).";
      case "unsupported": return "Браузер не поддерживает запись звука. Введите текст с клавиатуры.";
      case "denied": return "Нет доступа к микрофону. Разрешите его в настройках браузера или введите текст с клавиатуры.";
      default: return "Микрофон не найден или занят. Введите текст с клавиатуры.";
    }
  }
  if (cause instanceof ApiError) {
    if (cause.code === "dictation_busy") return "Распознавание занято. Повторите через несколько секунд.";
    if (cause.code === "dictation_unavailable") return "Диктовка сейчас недоступна. Введите текст с клавиатуры.";
    if (cause.code === "transition_not_allowed") return "Сейчас диктовка недоступна.";
    if (cause.status === 422) return "Запись слишком короткая или повреждена. Повторите.";
  }
  return "Не удалось распознать речь. Повторите или введите текст с клавиатуры.";
}

export function CallerChat({ item, open, onToggle, pending, rejected, onSend }: {
  item: IntakeItem; open: boolean; onToggle: () => void; pending: boolean; rejected: string | null;
  // input is "voice" when the text was dictated (ADR-037); typed messages pass nothing.
  onSend: (text: string, input?: "voice") => void;
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
  const dictation = item.dictation;
  const [phase, setPhase] = useState<"idle" | "recording" | "recognizing">("idle");
  const [dictationError, setDictationError] = useState<string | null>(null);
  const [voiceUsed, setVoiceUsed] = useState(false);
  const recorder = useRef<Recorder | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; recorder.current?.cancel(); recorder.current = null; };
  }, []);
  // The typed text is cleared only once the message actually lands in
  // the transcript (a new operator line), not at submit time — a
  // rejected send_caller_message leaves it in place to fix and resend
  // (review 2026-09-26, item 9). Same adjust-during-render pattern as
  // readCount below.
  const [sentAt, setSentAt] = useState<number | null>(null);
  if (sentAt !== null && operatorLines > sentAt) {
    setSentAt(null);
    setText("");
    setVoiceUsed(false);
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
    if (voiceUsed) onSend(trimmed, "voice"); else onSend(trimmed);
  };
  // Stops the recording and turns the phrase into text in the input box
  // (never sends it: the operator reads and edits it first).
  const finishDictation = async () => {
    const active = recorder.current;
    if (!active) return;
    recorder.current = null;
    setPhase("recognizing");
    try {
      const wav = await active.stop();
      const result = await dictate(item.id, wav);
      if (!mounted.current) return;
      const spoken = result.text.trim();
      if (!spoken) { setDictationError("Речь не распознана. Повторите."); return; }
      setText((current) => current.trim() ? `${current.trimEnd()} ${spoken}` : spoken);
      setVoiceUsed(true);
    } catch (cause) {
      if (mounted.current) setDictationError(dictationErrorText(cause));
    } finally {
      if (mounted.current) setPhase("idle");
    }
  };
  const toggleDictation = async () => {
    if (phase === "recording") { void finishDictation(); return; }
    if (phase !== "idle" || !dictation?.available) return;
    setDictationError(null);
    try {
      recorder.current = await startRecorder(dictation.max_seconds, () => { void finishDictation(); });
      if (!mounted.current) { recorder.current.cancel(); recorder.current = null; return; }
      setPhase("recording");
    } catch (cause) {
      setDictationError(dictationErrorText(cause));
    }
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
        onChange={(event) => { setText(event.target.value); if (!event.target.value.trim()) setVoiceUsed(false); }}
        onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); submit(); } }} />
      {phase === "recording" && <p className="caller-chat-dictation" role="status">Идёт запись… нажмите на микрофон, когда закончите.</p>}
      {phase === "recognizing" && <p className="caller-chat-dictation" role="status">Распознаю речь…</p>}
      {dictationError && <p className="caller-chat-dictation error" role="alert">{dictationError}</p>}
      <div className="caller-chat-input-row">
        {dictation?.available && <button type="button" className="caller-chat-mic" aria-pressed={phase === "recording"}
          aria-label={phase === "recording" ? "Остановить запись" : "Надиктовать сообщение"} title={phase === "recording" ? "Остановить запись" : "Надиктовать сообщение"}
          disabled={phase === "recognizing" || (phase === "idle" && !canType)} onClick={() => void toggleDictation()}><MicIcon size={16} /></button>}
        <span className={tooLong ? "caller-chat-counter error" : "caller-chat-counter"}>{charCount(text)} / {maxLength}</span>
        <button type="button" disabled={!canType || !trimmed || tooLong} onClick={submit}>Отправить</button>
      </div>
    </div>}
  </div>;
}
