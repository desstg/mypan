export type AdminRunStatusVariant = "success" | "error" | "pending" | "running";

export function normalizeRunStatusVariant(status: string): AdminRunStatusVariant {
  if (status === "scanning" || status === "executing" || status === "running") return "running";
  if (status === "success") return "success";
  if (status === "error") return "error";
  return "pending";
}
