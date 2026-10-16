import type { Component, Report, Requirement } from "./types";

export type Verdict = "pass" | "fail" | "manual" | "planned" | "unknown" | "n/a";

export function applies(r: Requirement, c: Component): boolean {
  const a = r.applies_to ?? {};
  if (!a.kinds?.length && !a.layers?.length && !a.components?.length) return true;
  return !!(a.kinds?.includes(c.kind) || a.layers?.includes(c.layer) || a.components?.includes(c.id));
}

/** How a requirement stands for one component, with the findings behind a
 * failure. Mirrors what the runtime check evaluates. */
export function evaluate(report: Report | null, r: Requirement, c: Component) {
  const none = { verdict: "n/a" as Verdict, findings: [] as string[] };
  if (!applies(r, c)) return none;
  if (c.status === "planned") return { verdict: "planned" as Verdict, findings: [] };
  if (c.status === "deprecated") return none;
  if (!r.verified_by) return { verdict: "manual" as Verdict, findings: [] };
  const [check, rule] = r.verified_by.split("/");
  // Runtime rules only inspect components that run in Compose.
  if (check === "runtime" && !c.runtime?.compose) return none;
  const result = report?.results.find((x) => x.check === check);
  if (!result || result.status === "skipped") return { verdict: "unknown" as Verdict, findings: [] };
  const findings = result.findings.filter((f) => f.component === c.id && (!rule || f.rule === rule)).map((f) => f.message);
  return { verdict: (findings.length ? "fail" : "pass") as Verdict, findings };
}
