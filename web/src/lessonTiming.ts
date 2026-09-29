// Bounds mirror internal/training's validateTiming (ADR-035); the server
// stays the authority, this only spares a round trip.
export function timingProblemText(open: number, primary: number, complete: number): string | null {
  if (![open, primary, complete].every(Number.isInteger)) return "Нормативы — целые числа секунд.";
  if (open < 10 || open > 300) return "Открытие карточки: от 10 до 300 с.";
  if (primary < 10 || primary > 600) return "Первичное решение: от 10 до 600 с (считается с момента открытия карточки).";
  if (complete < 60 || complete > 3600) return "Отработка: от 60 до 3600 с.";
  return null;
}
