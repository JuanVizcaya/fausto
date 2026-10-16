import type { Change, CodeModel, Info, Proposal, ProposalDetail, Report, Schema, Spec, Finding, JiraIssue, JiraOutcome, JiraReport, Task } from "./types";

/** An API error, with schema findings when the server rejected a spec. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly findings: Finding[] = [],
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    cache: "no-store",
    headers: body === undefined ? { "X-Fausto-Request": "1" } : { "Content-Type": "application/json", "X-Fausto-Request": "1" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    throw new ApiError(data?.error ?? `${path}: ${res.status} ${res.statusText}`, res.status, data?.findings ?? []);
  }
  return data as T;
}

const get = <T,>(path: string) => request<T>("GET", path);

export const fetchSpec = () => get<Spec>("/api/spec");
export const fetchReport = () => get<Report>("/api/check");
export const fetchInfo = () => get<Info>("/api/info");
export const fetchSchemas = () => get<Record<string, Schema>>("/api/schemas");
export const fetchCode = (component: string) => get<CodeModel>(`/api/code/${encodeURIComponent(component)}`);
export const fetchProposals = () => get<Proposal[]>("/api/proposals");
export const fetchProposal = (id: string) => get<ProposalDetail>(`/api/proposals/${encodeURIComponent(id)}`);
export const createProposal = (title: string, description: string, milestone: string, changes: Change[]) =>
  request<ProposalDetail>("POST", "/api/proposals", { title, description, milestone, changes });
export const approveProposal = (id: string) =>
  request<Proposal & { jiraSync?: JiraOutcome }>("POST", `/api/proposals/${encodeURIComponent(id)}/approve`);
export const rejectProposal = (id: string) => request<Proposal>("POST", `/api/proposals/${encodeURIComponent(id)}/reject`);
/** Sets the status of a task for a person; fausto commits it to the base branch. */
export const setTaskStatus = (id: string, status: Task["status"]) =>
  request<Task & { jiraSync?: JiraOutcome }>("POST", `/api/tasks/${encodeURIComponent(id)}/status`, { status });
/** The tasks' issues as they are in Jira right now. */
export const fetchJiraIssues = () => get<JiraIssue[]>("/api/jira/issues");
/** Mirrors every task as a Jira issue. */
export const syncJira = () => request<JiraReport>("POST", "/api/jira/sync");

/** Why a best-effort Jira sync fell short, or null if it didn't. */
export function jiraProblem(o?: JiraOutcome): string | null {
  if (!o) return null;
  if (o.error) return o.error;
  const failed = o.report?.results.filter((r) => r.error) ?? [];
  if (failed.length === 0) return null;
  return failed.map((r) => `${r.task}: ${r.error}`).join("; ");
}

/** Findings about one component, across all checks. */
export function componentFindings(report: Report | null, id: string) {
  if (!report) return [];
  return report.results.flatMap((r) =>
    r.findings.filter((f) => f.component === id).map((f) => ({ ...f, check: r.check })),
  );
}
