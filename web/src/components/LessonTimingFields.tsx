export function TimingFields({ open, primary, complete, onOpen, onPrimary, onComplete }: {
  open: string; primary: string; complete: string;
  onOpen: (value: string) => void; onPrimary: (value: string) => void; onComplete: (value: string) => void;
}) {
  return (
    <fieldset className="timing-fields">
      <legend>Нормативы времени, с</legend>
      <label>Открыть карточку<input type="number" min={10} max={300} required value={open} onChange={(event) => onOpen(event.target.value)} /></label>
      <label>Первичное решение<input type="number" min={10} max={600} required value={primary} onChange={(event) => onPrimary(event.target.value)} /></label>
      <label>Отработка после решения<input type="number" min={60} max={3600} required value={complete} onChange={(event) => onComplete(event.target.value)} /></label>
    </fieldset>
  );
}
