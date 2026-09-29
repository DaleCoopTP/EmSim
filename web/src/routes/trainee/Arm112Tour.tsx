import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import "./Arm112Tour.css";

type Callout = { target: string; text: string };
type TourStep = readonly Callout[];

const introSteps: readonly TourStep[] = [
  [{ target: "help", text: "Опция “Сообщить о проблеме” позволяет направить сообщение в СТП" }],
  [{ target: "auto-update", text: "Автообновление должно быть включено, чтобы карточки приходили своевременно" }],
  [{ target: "grid", text: "Список карточек (грид)" }],
];

// The VIS-specific annotations in the reference image belong to a different card type.
const cardSteps: readonly TourStep[] = [
  [
    { target: "preview-toggle", text: "Открывает предпросмотр карточки" },
    { target: "linked-cards", text: "Наличие связанных карточек" },
    { target: "pin-card", text: "Закрепить карточку на экране" },
  ],
  [
    { target: "important-card", text: "Важное происшествие (устанавливается Службой 112)" },
    { target: "timer", text: "Таймер" },
    { target: "operator-number", text: "№ оператора Службы 112" },
  ],
  [
    { target: "workstation-number", text: "№ АРМ в Службе 112" },
    { target: "card-number", text: "№ Карточки происшествия" },
    { target: "creation-date", text: "Дата создания карточки" },
  ],
  [
    { target: "creation-time", text: "Время создания карточки" },
    { target: "questionnaire", text: "Опросная карта (выбирается оператором Службы 112)" },
    { target: "victims", text: "Наличие пострадавших" },
  ],
  [
    { target: "card-status", text: "Статус карточки" },
    { target: "address", text: "Адрес" },
    { target: "control-indicator", text: "Индикатор контроля" },
  ],
  [
    { target: "verification", text: "Проверка карточки" },
    { target: "preview-services", text: "Список оповещения" },
    { target: "preview-applicant", text: "Информация о заявителе (ФИО, телефон)" },
  ],
  [
    { target: "preview-information", text: "Признаки происшествия (выбираются при заполнении опросной карты оператором Службы 112)" },
    { target: "preview-activity", text: "Звонки оператора Службы 112" },
  ],
];

type Spotlight = { top: number; right: number; bottom: number; left: number; text: string };
type Layout = { width: number; height: number; spots: Spotlight[] };
type Tip = { top: number; left: number; width: number; below: boolean };

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(value, max));
}

function tipPositions(layout: Layout): Tip[] {
  const { spots, width, height } = layout;
  const widthOfTip = Math.min(spots.length === 1 ? 340 : 240, width - 32);
  const minTop = Math.min(...spots.map((spot) => spot.top));
  const maxBottom = Math.max(...spots.map((spot) => spot.bottom));
  const slotHeight = spots.length === 1 ? 88 : 100;
  const totalHeight = spots.length * slotHeight;
  const belowRoom = height - maxBottom - 82; // Leave the bottom navigation clear.
  const below = spots.length === 1 ? belowRoom >= 150 : belowRoom >= totalHeight || belowRoom >= minTop - 16;
  const start = below ? maxBottom + 16 : Math.max(16, minTop - totalHeight - 16);
  return spots.map((spot, index) => ({
    top: clamp(start + index * slotHeight, 16, height - slotHeight - 16),
    left: clamp((spot.left + spot.right - widthOfTip) / 2, 16, width - widthOfTip - 16),
    width: widthOfTip,
    below,
  }));
}

export function Arm112Tour({ rootRef, onClose, hasCard, onPreviewStep }: {
  rootRef: RefObject<HTMLElement>; onClose: () => void; hasCard: boolean; onPreviewStep: (open: boolean) => void;
}) {
  const tourSteps = hasCard ? [...introSteps, ...cardSteps] : introSteps;
  const [step, setStep] = useState(0);
  const [layout, setLayout] = useState<Layout | null>(null);
  const nextRef = useRef<HTMLButtonElement>(null);
  const ready = layout !== null;
  const goToStep = (next: number) => {
    // The underlying page is inert in tour mode; reveal the preview automatically.
    onPreviewStep(hasCard && next >= introSteps.length + 5);
    setStep(next);
  };

  useEffect(() => {
    const app = document.getElementById("root");
    const wasInert = app?.inert;
    if (app) app.inert = true;
    return () => { if (app) app.inert = wasInert ?? false; };
  }, []);

  useLayoutEffect(() => {
    const callouts = tourSteps[step];
    if (!callouts) { onClose(); return; }
    const targets = callouts.map((callout) => rootRef.current?.querySelector<HTMLElement>(`[data-tour-target="${callout.target}"]`));
    if (targets.some((target) => !target)) {
      onClose();
      return;
    }
    const elements = targets as HTMLElement[];
    const update = () => {
      const width = window.innerWidth;
      const height = window.innerHeight;
      setLayout({ width, height, spots: elements.map((element, index) => {
        const rect = element.getBoundingClientRect();
        const left = clamp(rect.left - 5, 0, width);
        const top = clamp(rect.top - 5, 0, height);
        return {
          left, top,
          right: clamp(rect.right + 5, left, width),
          bottom: clamp(rect.bottom + 5, top, height),
          text: callouts[index].text,
        };
      }) });
    };
    update();
    const observer = new ResizeObserver(update);
    elements.forEach((element) => observer.observe(element));
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

  if (!layout) return null;
  const tips = tipPositions(layout);
  return createPortal(
    <div className="arm112-tour" role="dialog" aria-modal="true" aria-label="Ознакомительный режим">
      <svg className="arm112-tour-shade" width={layout.width} height={layout.height} aria-hidden="true">
        <defs><mask id="arm112-tour-holes" maskUnits="userSpaceOnUse">
          <rect width={layout.width} height={layout.height} fill="white" />
          {layout.spots.map((spot, index) => <rect key={index} x={spot.left} y={spot.top}
            width={spot.right - spot.left} height={spot.bottom - spot.top} fill="black" />)}
        </mask></defs>
        <rect width={layout.width} height={layout.height} fill="rgb(10 17 22 / 76%)" mask="url(#arm112-tour-holes)" />
      </svg>
      <svg className="arm112-tour-leaders" width={layout.width} height={layout.height} aria-hidden="true">
        {layout.spots.map((spot, index) => <line key={index}
          x1={(spot.left + spot.right) / 2} y1={tips[index].below ? spot.bottom : spot.top}
          x2={tips[index].left + tips[index].width / 2} y2={tips[index].below ? tips[index].top : tips[index].top + 82} />)}
      </svg>
      {layout.spots.map((spot, index) => <div key={index} className="arm112-tour-spotlight" style={{
        top: spot.top, left: spot.left, width: spot.right - spot.left, height: spot.bottom - spot.top,
      }} />)}
      {layout.spots.map((spot, index) => <div key={index} className="arm112-tour-tip" style={{
        top: tips[index].top, left: tips[index].left, width: tips[index].width,
      }}><p>{spot.text}</p></div>)}
      <button type="button" className="arm112-tour-close" aria-label="Закрыть ознакомительный режим" onClick={onClose}>×</button>
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
