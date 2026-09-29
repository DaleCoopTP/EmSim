// ARM-112 shows short numeric card numbers (e.g. 38260204). A 112 training
// card's own number is "112-<item uuid>", unreadable in a grid, so the
// screens show an eight-digit number derived from the uuid instead. It is a
// display value only: the API and the instructor screens keep the full one.
export function armCardNumber(number: string): string {
  const match = /^112-([0-9a-f]{8})/i.exec(number);
  if (!match) return number;
  return String(10_000_000 + (parseInt(match[1], 16) % 90_000_000));
}

// The "Опер." column shows an operator's own number; a training account has
// none, so a stable three-digit one is derived from the user id.
export function armOperatorNumber(userID: string): string {
  const hex = userID.replace(/[^0-9a-f]/gi, "").slice(0, 8) || "0";
  return String(100 + (parseInt(hex, 16) % 900));
}

// "+74995503456" -> "+7 (499) 550-34-56"; anything else is shown as is.
export function formatPhone(value: string): string {
  const digits = value.replace(/\D/g, "");
  if (digits.length !== 11 || !/^[78]/.test(digits)) return value;
  return `+7 (${digits.slice(1, 4)}) ${digits.slice(4, 7)}-${digits.slice(7, 9)}-${digits.slice(9)}`;
}

// "Иванова Елена Петровна" -> "Иванова Е. П.", as the reference prints the operator.
export function armShortName(fullName: string): string {
  const [last, ...rest] = fullName.trim().split(/\s+/);
  return [last, ...rest.map((part) => `${part.charAt(0)}.`)].join(" ");
}
