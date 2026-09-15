export function normalizeBoolSetting(value: string): string {
  const s = String(value ?? "").trim().toLowerCase();
  return s === "true" || s === "1" || s === "yes" || s === "on" ? "true" : "false";
}

export function settingItemChanged(type: string, formVal: string, origVal: string): boolean {
  if (type === "bool") {
    return normalizeBoolSetting(formVal) !== normalizeBoolSetting(origVal);
  }
  if (type === "int") {
    return Number(formVal) !== Number(origVal);
  }
  return formVal !== origVal;
}

export function parseSettingNumber(value: unknown): number {
  const n = Number(value);
  return Number.isFinite(n) ? n : 0;
}
