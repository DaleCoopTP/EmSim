import type { IntakeProfileFieldDef } from "./routes/trainee/Operator112Workplace";

// profileFieldVisible mirrors content.IntakeFieldVisible: a conditional
// field (card 101's "Где" branches) is shown only while the field it
// depends on holds one of the listed known values.
export function profileFieldVisible(field: Pick<IntakeProfileFieldDef, "visible_when">,
  answers: Record<string, { state: string; value?: string; values?: string[] } | undefined> | undefined): boolean {
  if (!field.visible_when) return true;
  const parent = answers?.[field.visible_when.field_id];
  if (parent?.state !== "known") return false;
  const values = parent.value !== undefined && parent.value !== "" ? [parent.value] : parent.values ?? [];
  return values.some((value) => field.visible_when!.any_of.includes(value));
}

// expectedProfileText renders one intake112.reference.expected_profiles
// value: a string, a set of options, or {"state":"unknown"}.
export function expectedProfileText(value: unknown): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) return value.join(", ");
  if (value && typeof value === "object" && (value as { state?: string }).state === "unknown") return "Неизвестно";
  return "—";
}
