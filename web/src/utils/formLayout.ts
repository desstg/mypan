import type { FieldSchema } from "@/api/types";

export type FormLayoutRow = { mode: "full" | "half"; fields: FieldSchema[] };

function isFullWidthField(f: FieldSchema): boolean {
  if (f.full_width) return true;
  if (f.type === "local_dir") return true;
  const name = f.name.toLowerCase();
  const label = f.label.toLowerCase();
  if (name === "api_url") return true;
  if (name.includes("cookie") || label.includes("cookie")) return true;
  return false;
}

export function buildFormLayoutRows(fields: FieldSchema[]): FormLayoutRow[] {
  const rows: FormLayoutRow[] = [];
  const pending: FieldSchema[] = [];

  const flushPairs = () => {
    while (pending.length >= 2) {
      rows.push({ mode: "half", fields: pending.splice(0, 2) });
    }
  };

  // pair_key 相同的字段紧挨排列，避免 struct 字段顺序干扰配对
  const ordered: FieldSchema[] = [];
  const seenPair = new Set<string>();
  for (const f of fields) {
    if (!f.pair_key) {
      ordered.push(f);
      continue;
    }
    if (seenPair.has(f.pair_key)) continue;
    for (const x of fields) {
      if (x.pair_key === f.pair_key) ordered.push(x);
    }
    seenPair.add(f.pair_key);
  }

  let i = 0;
  while (i < ordered.length) {
    const field = ordered[i];
    const next = ordered[i + 1];

    if (field.pair_key && next?.pair_key === field.pair_key) {
      flushPairs();
      rows.push({ mode: "half", fields: [field, next] });
      i += 2;
      continue;
    }

    if (isFullWidthField(field)) {
      flushPairs();
      if (pending.length) {
        rows.push({ mode: "half", fields: [pending.pop()!] });
      }
      rows.push({ mode: "full", fields: [field] });
      i += 1;
      continue;
    }

    pending.push(field);
    flushPairs();
    i += 1;
  }

  flushPairs();
  if (pending.length) {
    rows.push({ mode: "half", fields: [pending.pop()!] });
  }

  return rows;
}
