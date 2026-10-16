import type { Level, Risk } from "./types";

const value: Record<Level, number> = { low: 1, medium: 2, high: 3 };

export const severity = (r: Risk) => value[r.likelihood] * value[r.impact];

export function severityLabel(score: number) {
  if (score >= 9) return "critical";
  if (score >= 6) return "high";
  if (score >= 3) return "medium";
  return "low";
}

/** Live risks are the ones still worth attention. */
export const live = (r: Risk) => r.status === "open" || r.status === "mitigating";

/** True when review_by (YYYY-MM) is before the current month. */
export function overdue(r: Risk, now = new Date()) {
  if (!r.review_by || !live(r)) return false;
  const current = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`;
  return r.review_by < current;
}
