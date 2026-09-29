import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import "./Arm112Tour.css";

const steps = [
  {
    target: "help",
    text: "Опция “Сообщить о проблеме” позволяет направить сообщение в СТП",
  },
  {
    target: "auto-update",
    text: "Автообновление должно быть включено, чтобы карточки приходили своевременно",
  },
  {
    target: "grid",
    text: "Список карточек (грид)",
  },
] as const;

const cardSteps = [
  { target: "preview-toggle", text: "Открывает предпросмотр карточки" },
  { target: "preview", text: "В предпросмотре видны службы, заявитель, информация о происшествии и действия оператора" },
] as const;

type Spotlight = {
  top: number;
  right: number;
  bottom: number;
  left: number;
  width: number;
  height: number;
};

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(value, max));
}

export function Arm112Tour({ rootRef, onClose, hasCard, onPreviewStep }: {
  rootRef: RefObject<HTMLElement>; onClose: () => void; hasCard: boolean; onPreviewStep: (open: boolean) => void;
}) {
  const tourSteps: readonly { target: string; text: string }[] = hasCard ? [...steps, ...cardSteps] : steps;
  const [step, setStep] = useState(0);
  const [spotlight, setSpotlight] = useState<Spotlight | null>(null);
  const nextRef = useRef<HTMLButtonElement>(null);
  const ready = spotlight !== null;
  const goToStep = (next: number) => {
    onPreviewStep(hasCard && next === steps.length + 1);
    setStep(next);
  };

  useEffect(() => {
    const app = document.getElementById("root");
    const wasInert = app?.inert;
    if (app) app.inert = true;
    return () => {
      if (app) app.inert = wasInert ?? false;
    };
  }, []);

  useLayoutEffect(() => {
    const target = rootRef.current?.querySelector<HTMLElement>(`[data-tour-target="${tourSteps[step].target}"]`);
    if (!target) {
      onClose();
      return;
    }
    const update = () => {
      const rect = target.getBoundingClientRect();
      const left = clamp(rect.left - 6, 0, window.innerWidth);
      const right = clamp(rect.right + 6, left, window.innerWidth);
      const top = clamp(rect.top - 6, 0, window.innerHeight);
      const bottom = clamp(rect.bottom + 6, top, window.innerHeight);
      setSpotlight({ top, right, bottom, left, width: window.innerWidth, height: window.innerHeight });
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(target);
    window.addEventListener("resize", update);
    window.addEventListener("scroll", update, true);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", update);
      window.removeEventListener("scroll", update, true);
    };
  }, [rootRef, step, onClose, hasCard]);

  useEffect(() => {
    if (ready) nextRef.current?.focus();
  }, [ready]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
      if (event.key === "ArrowLeft" && step > 0) {
        event.preventDefault();
        goToStep(step - 1);
      }
      if (event.key === "ArrowRight") {
        event.preventDefault();
        if (step === tourSteps.length - 1) onClose();
        else goToStep(step + 1);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose, step, hasCard, onPreviewStep]);

  if (!spotlight) return null;
  const tooltipWidth = Math.min(340, spotlight.width - 32);
  const tooltipLeft = clamp((spotlight.left + spotlight.right - tooltipWidth) / 2, 16, spotlight.width - tooltipWidth - 16);
  const tooltipBelow = spotlight.height - spotlight.bottom >= 150;
  const tooltipTop = tooltipBelow
    ? spotlight.bottom + 14
    : Math.max(16, spotlight.top - 112);

  return createPortal(
    <div className="arm112-tour" role="dialog" aria-modal="true" aria-label="Ознакомительный режим">
      <div className="arm112-tour-shade" style={{ top: 0, left: 0, width: spotlight.width, height: spotlight.top }} />
      <div className="arm112-tour-shade" style={{ top: spotlight.top, left: 0, width: spotlight.left, height: spotlight.bottom - spotlight.top }} />
      <div className="arm112-tour-shade" style={{ top: spotlight.top, left: spotlight.right, width: spotlight.width - spotlight.right, height: spotlight.bottom - spotlight.top }} />
      <div className="arm112-tour-shade" style={{ top: spotlight.bottom, left: 0, width: spotlight.width, height: spotlight.height - spotlight.bottom }} />
      <div className="arm112-tour-spotlight" style={{
        top: spotlight.top, left: spotlight.left, width: spotlight.right - spotlight.left, height: spotlight.bottom - spotlight.top,
      }} />
      {tooltipBelow && <div className="arm112-tour-leader" style={{
        top: spotlight.bottom, left: (spotlight.left + spotlight.right) / 2, height: tooltipTop - spotlight.bottom,
      }} />}
      <div className="arm112-tour-tip" style={{ top: tooltipTop, left: tooltipLeft, width: tooltipWidth }}>
        <button type="button" className="arm112-tour-close" aria-label="Закрыть ознакомительный режим" onClick={onClose}>×</button>
        <p>{tourSteps[step].text}</p>
      </div>
      <nav className="arm112-tour-navigation" aria-label="Шаги ознакомительного режима">
        <button type="button" aria-label="Предыдущее объяснение" disabled={step === 0} onClick={() => goToStep(step - 1)}>←</button>
        <span aria-live="polite">{step + 1} / {tourSteps.length}</span>
        <button ref={nextRef} type="button" aria-label={step === tourSteps.length - 1 ? "Завершить ознакомительный режим" : "Следующее объяснение"}
          onClick={() => step === tourSteps.length - 1 ? onClose() : goToStep(step + 1)}>→</button>
      </nav>
    </div>,
    document.body,
  );
}
