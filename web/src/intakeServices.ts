// Display names shared by the operator 112 card and the instructor's
// reference card: the pilot services and the applicant statuses.
export const serviceNames: Record<string, string> = {
  pilot_gas_104: "104 · Газовая служба (учебная)",
  pilot_fire_101: "101 · Пожарная служба (учебная)",
  pilot_ambulance: "03 · Скорая помощь (учебная)",
};
// Short tile captions for the orange services bar, as in the reference card.
export const serviceTiles: Record<string, string> = { pilot_gas_104: "Служба 104", pilot_fire_101: "Служба 101", pilot_ambulance: "Скорая" };
export const applicantStatuses = [["witness", "очевидец"], ["victim", "пострадавший"], ["relative", "родственник"],
  ["acquaintance", "знакомый"], ["child", "ребёнок"], ["participant", "участник"]] as const;
