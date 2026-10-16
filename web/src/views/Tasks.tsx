import { useEffect, useMemo, useState } from "react";
import type { ViewProps } from "../App";
import { fetchJiraIssues, jiraProblem, setTaskStatus, syncJira } from "../api";
import type { JiraIssue, JiraReport, Task } from "../types";
import { Code, Empty, Markdown, Pill, Section, StatusPill } from "../ui";
import { KindIcon, kindLabel } from "./Agents";

const order = { open: 0, "in-progress": 1, done: 2, dropped: 3 } as const;

type StatusFilter = "active" | Task["status"] | "all";

const statusFilters: [StatusFilter, string, (t: Task) => boolean][] = [
  ["active", "Active", (t) => t.status === "open" || t.status === "in-progress"],
  ["open", "Open", (t) => t.status === "open"],
  ["in-progress", "In progress", (t) => t.status === "in-progress"],
  ["done", "Done", (t) => t.status === "done"],
  ["dropped", "Dropped", (t) => t.status === "dropped"],
  ["all", "All", () => true],
];

export function TasksView({ spec, item, go, reload, info }: ViewProps) {
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<StatusFilter>("active");
  const [agent, setAgent] = useState("");
  const [milestone, setMilestone] = useState("");
  const [syncing, setSyncing] = useState(false);
  const [sync, setSync] = useState<{ report?: JiraReport; error?: string } | null>(null);
  const jira = info?.jira;
  // Live issue details from Jira, by task id; refetched whenever the spec
  // reloads (after a sync or a status change, for example).
  const [issues, setIssues] = useState<Map<string, JiraIssue>>(new Map());
  const [issuesError, setIssuesError] = useState<string | null>(null);
  useEffect(() => {
    if (!jira) return;
    let live = true;
    fetchJiraIssues()
      .then((list) => {
        if (!live) return;
        setIssues(new Map(list.filter((i) => i.task).map((i) => [i.task!, i])));
        setIssuesError(null);
      })
      .catch((e) => live && setIssuesError(e instanceof Error ? e.message : String(e)));
    return () => {
      live = false;
    };
  }, [jira, spec]);
  const runSync = async () => {
    setSyncing(true);
    setSync(null);
    try {
      setSync({ report: await syncJira() });
    } catch (e) {
      setSync({ error: e instanceof Error ? e.message : String(e) });
    } finally {
      setSyncing(false);
      await reload();
    }
  };

  const tasks = useMemo(
    () =>
      [...spec.tasks].sort(
        (a, b) => order[a.status] - order[b.status] || b.created.localeCompare(a.created) || a.agent.localeCompare(b.agent),
      ),
    [spec.tasks],
  );
  const agentOf = (id: string) => spec.agents.find((x) => x.id === id);
  const milestoneOf = (id?: string) => spec.roadmap.milestones.find((m) => m.id === id);
  const matchStatus = statusFilters.find(([k]) => k === status)![2];
  const q = query.trim().toLowerCase();
  const shown = tasks.filter((t) => {
    if (!matchStatus(t) || (agent && t.agent !== agent) || (milestone && t.milestone !== milestone)) return false;
    if (!q) return true;
    const a = agentOf(t.agent);
    return [t.id, t.title, t.body, t.agent, a?.name ?? "", t.proposal ?? "", t.milestone ?? "", ...(t.components ?? [])].some((s) =>
      s.toLowerCase().includes(q),
    );
  });
  const selected = tasks.find((t) => t.id === item);
  const filtered = q || status !== "active" || agent || milestone;
  const clear = () => {
    setQuery("");
    setStatus("active");
    setAgent("");
    setMilestone("");
  };
  // Agents and milestones that have tasks, for the filter menus.
  const agents = spec.agents.filter((a) => tasks.some((t) => t.agent === a.id));
  const milestones = spec.roadmap.milestones.filter((m) => tasks.some((t) => t.milestone === m.id));

  return (
    <div className="split split-tasks">
      <Section title="Agent tasks">
        <p className="lede">
          Accepting a proposal gives each affected agent a task, kept in fausto's database. Agents pull theirs with{" "}
          <Code>fausto task pull</Code> and report progress with <Code>fausto task start</Code> and <Code>done</Code>. People set the
          status of their own tasks here.
          {jira && (
            <>
              {" "}
              Each task has an issue in Jira project <Code>{jira.project}</Code>, kept at the task's status.
            </>
          )}
        </p>
        <div className="filters" role="search">
          <input
            type="search"
            className="search"
            placeholder="Search title, text, agent, component…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search tasks"
          />
          <div className="segmented" role="radiogroup" aria-label="Status">
            {statusFilters.map(([k, label, match]) => (
              <button key={k} role="radio" aria-checked={status === k} className={status === k ? "active" : ""} onClick={() => setStatus(k)}>
                {label} {tasks.filter(match).length}
              </button>
            ))}
          </div>
          <select className="select" value={agent} onChange={(e) => setAgent(e.target.value)} aria-label="Agent">
            <option value="">All agents</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
          {milestones.length > 0 && (
            <select className="select" value={milestone} onChange={(e) => setMilestone(e.target.value)} aria-label="Milestone">
              <option value="">All milestones</option>
              {milestones.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.title}
                </option>
              ))}
            </select>
          )}
          {jira && info?.editable && (
            <button className="btn btn-small" disabled={syncing} onClick={runSync}>
              {syncing ? "Syncing…" : "Sync with Jira"}
            </button>
          )}
        </div>
        {sync && <SyncSummary {...sync} />}
        {issuesError && <p className="hint">Couldn't load the issues from Jira: {issuesError}</p>}
        {tasks.length === 0 ? (
          <Empty>No tasks yet. Accepting a proposal creates one for each affected agent.</Empty>
        ) : shown.length === 0 ? (
          <Empty>
            No tasks match.{" "}
            <button className="link" onClick={clear}>
              Clear the filters
            </button>
          </Empty>
        ) : (
          <ul className="agent-list">
            {shown.map((t) => {
              const a = agentOf(t.agent);
              const m = milestoneOf(t.milestone);
              const isSel = selected?.id === t.id;
              const issue = issues.get(t.id);
              return (
                <li key={t.id}>
                  <button
                    className={`agent-row task-row kind-${a?.kind ?? "agent"} status-${t.status}${isSel ? " active" : ""}`}
                    onClick={() => go("tasks", isSel ? undefined : t.id)}
                    aria-current={isSel}
                  >
                    <KindIcon kind={a?.kind ?? "agent"} />
                    <span className="agent-main">
                      <span className="agent-name">{t.title}</span>
                      <span className="agent-scope">
                        {a?.name ?? t.agent} · {t.created}
                        {t.jira && ` · ${t.jira}`}
                        {issue && ` · ${issue.assignee ?? "Unassigned"}`}
                        {m && ` · ${m.title}`}
                        {!!t.components?.length && ` · ${t.components.join(", ")}`}
                      </span>
                    </span>
                    <span className="agent-stats">
                      <StatusPill status={t.status} />
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {filtered && shown.length > 0 && (
          <p className="hint">
            Showing {shown.length} of {tasks.length}.{" "}
            <button className="link" onClick={clear}>
              Clear the filters
            </button>
          </p>
        )}
      </Section>
      <aside className="detail">
        {selected ? (
          <TaskDetail key={selected.id} t={selected} issue={issues.get(selected.id)} spec={spec} go={go} reload={reload} info={info} />
        ) : <Empty>Select a task to see what the agent has to do.</Empty>}
      </aside>
    </div>
  );
}

const statuses: [Task["status"], string][] = [
  ["open", "Open"],
  ["in-progress", "In progress"],
  ["done", "Done"],
  ["dropped", "Dropped"],
];

function TaskDetail({
  t,
  issue,
  spec,
  go,
  reload,
  info,
}: { t: Task; issue?: JiraIssue } & Pick<ViewProps, "spec" | "go" | "reload" | "info">) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const a = spec.agents.find((x) => x.id === t.agent);
  // Agents set their own status with `fausto task`; people set it here.
  const settable = a?.kind === "human" && !!info?.editable;
  const changeStatus = async (status: Task["status"]) => {
    if (status === t.status) return;
    setBusy(true);
    setError(null);
    try {
      const problem = jiraProblem((await setTaskStatus(t.id, status)).jiraSync);
      if (problem) setError(`Status saved, but Jira was not updated: ${problem}`);
      await reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const m = spec.roadmap.milestones.find((x) => x.id === t.milestone);
  const components = (t.components ?? []).map((id) => spec.system.components.find((c) => c.id === id) ?? { id, name: id });
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{t.title}</h3>
        <StatusPill status={t.status} />
      </div>
      <dl className="facts">
        <dt>For</dt>
        <dd className="agent-title">
          <KindIcon kind={a?.kind ?? "agent"} />
          <button className="link" onClick={() => go("agents", t.agent)}>
            {a?.name ?? t.agent}
          </button>
          <Pill tone={a?.kind === "human" ? "warn" : "info"}>{kindLabel(a?.kind ?? "agent")}</Pill>
        </dd>
        {settable && (
          <>
            <dt>Status</dt>
            <dd>
              <div className="segmented" role="radiogroup" aria-label="Task status">
                {statuses.map(([k, label]) => (
                  <button
                    key={k}
                    role="radio"
                    aria-checked={t.status === k}
                    className={t.status === k ? "active" : ""}
                    disabled={busy}
                    onClick={() => changeStatus(k)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </dd>
          </>
        )}
        <dt>Created</dt>
        <dd>{t.created}</dd>
        {m && (
          <>
            <dt>Milestone</dt>
            <dd>
              <a href={`#/roadmap/${m.id}`}>{m.title}</a>
            </dd>
          </>
        )}
        {t.jira && (
          <>
            <dt>Jira</dt>
            <dd>
              {info?.jira ? (
                <a href={`${info.jira.url}/browse/${t.jira}`} target="_blank" rel="noreferrer">
                  {t.jira}
                </a>
              ) : (
                <Code>{t.jira}</Code>
              )}
              {issue && (
                <>
                  {" "}
                  <Pill tone={drifted(issue) ? "warn" : "neutral"}>{issue.status}</Pill>
                  {issue.priority && <span className="muted small"> · {issue.priority} priority</span>}
                </>
              )}
              {issue && drifted(issue) && (
                <div className="muted small">
                  Jira says {issue.status}; the task says {t.status}. The next sync moves the issue to {issue.want}.
                </div>
              )}
            </dd>
          </>
        )}
        {issue && (
          <>
            <dt>Assignee</dt>
            <dd className="agent-title">
              {issue.avatar && (
                <img
                  src={issue.avatar}
                  alt=""
                  width={20}
                  height={20}
                  className="avatar"
                  onError={(e) => (e.currentTarget.style.display = "none")}
                />
              )}
              {issue.assignee ?? <span className="muted">Unassigned</span>}
            </dd>
            {issue.updated && (
              <>
                <dt>Updated</dt>
                <dd>{new Date(issue.updated).toLocaleString()}</dd>
              </>
            )}
          </>
        )}
        {t.proposal && (
          <>
            <dt>Proposal</dt>
            <dd>
              <a href={`#/proposals/${t.proposal}`}>{t.proposal}</a>
            </dd>
          </>
        )}
        {components.length > 0 && (
          <>
            <dt>Components</dt>
            <dd className="chips">
              {components.map((c) => (
                <a key={c.id} className="chip" href={`#/architecture/${c.id}`}>
                  {c.name}
                </a>
              ))}
            </dd>
          </>
        )}
        {!!t.history?.length && (
          <>
            <dt>History</dt>
            <dd>
              <ul className="plain small">
                {t.history.map((h, i) => (
                  <li key={i}>
                    {new Date(h.at).toLocaleString()}: {h.from} → {h.to} by {h.by.replace(/\s*<.*>$/, "")}
                  </li>
                ))}
              </ul>
            </dd>
          </>
        )}
      </dl>
      {error && <div className="banner bad">{error}</div>}
      {settable ? (
        <p className="muted small">
          A person works this task. Set its status above{info?.jira ? "; fausto moves its Jira issue to match" : ""}.
        </p>
      ) : (
        (t.status === "open" || t.status === "in-progress") &&
        (a?.kind === "human" ? (
          <p className="muted small">A person works this task by hand, then sets its status to done.</p>
        ) : (
          <RunTask agent={t.agent} project={spec.system.name} />
        ))
      )}
      <Markdown source={t.body} />
    </div>
  );
}

/** Whether Jira's status differs from the one the task's status maps to. */
function drifted(issue: JiraIssue) {
  return !!issue.want && issue.status.toLowerCase() !== issue.want.toLowerCase();
}

function SyncSummary({ report, error }: { report?: JiraReport; error?: string }) {
  if (error && !report) return <div className="banner bad">Jira sync failed: {error}</div>;
  if (!report) return null;
  const count = (action: string) => report.results.filter((r) => r.actions.includes(action)).length;
  const failed = report.results.filter((r) => r.error);
  const parts = [
    [count("created"), "created"],
    [count("linked"), "linked"],
    [count("transitioned"), "moved"],
    [count("ok"), "already in line"],
  ].filter(([n]) => n) as [number, string][];
  return (
    <div className={`banner ${failed.length || error ? "bad" : "good"}`}>
      Jira sync: {parts.map(([n, label]) => `${n} ${label}`).join(", ") || "no tasks"}
      {failed.length > 0 && `, ${failed.length} failed`}.
      {error && <> {error}</>}
      {failed.length > 0 && (
        <ul>
          {failed.map((r) => (
            <li key={r.task}>
              <Code>{r.task}</Code>: {r.error}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** How to start the agent on its tasks: its own branch and work tree of the
 * project, its tasks pulled from fausto's database, and a Claude Code session
 * with the agent definition from .claude/agents. */
export function RunTask({ agent, project, title = "Run this task" }: { agent: string; project: string; title?: string }) {
  const [copied, setCopied] = useState(false);
  const commands = [
    `git worktree add ../${project}-${agent} -b agent/${agent}   # once; later: git -C ../${project}-${agent} merge dev`,
    `cd ../${project}-${agent}`,
    `fausto task pull ${agent}   # writes .fausto/tasks/`,
    `claude --agent ${agent} "Work your open tasks."`,
  ].join("\n");
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(commands);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard unavailable; the commands stay selectable.
    }
  };
  return (
    <div className="run-task">
      <div className="run-task-head">
        <strong>{title}</strong>
        <button className="btn btn-small" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <p className="muted">
        From the repository root, start the agent in its own branch with its tasks pulled from fausto (it needs{" "}
        <Code>FAUSTO_MONGO_URI</Code> in its environment). It works all its open tasks, then you review with{" "}
        <Code>{`git diff dev...agent/${agent}`}</Code> and merge.
      </p>
      <pre className="mono run-task-cmd">{commands}</pre>
    </div>
  );
}
