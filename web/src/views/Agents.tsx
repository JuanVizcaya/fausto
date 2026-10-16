import { useMemo, useState } from "react";
import type { ViewProps } from "../App";
import { EditButton } from "../editor";
import type { Agent, Finding, Spec } from "../types";
import { Code, Empty, Pill, Section, StatusPill } from "../ui";
import { RunTask } from "./Tasks";

type KindFilter = "all" | "agent" | "human";

const live = (status: string) => status === "open" || status === "in-progress";

/** What the list and the detail show about one agent. */
function summarize(a: Agent, spec: Spec, report: ViewProps["report"], proposals: ViewProps["proposals"]) {
  const components = spec.system.components.filter((c) => c.owner === a.id);
  const tasks = spec.tasks.filter((t) => t.agent === a.id);
  const definition = `.claude/agents/${a.id}.md`;
  const findings: (Finding & { check: string })[] =
    report?.results.flatMap((r) =>
      r.findings
        .filter((f) => f.path === a.file || f.path === definition || components.some((c) => c.id === f.component))
        .map((f) => ({ ...f, check: r.check })),
    ) ?? [];
  return {
    components,
    tasks,
    openTasks: tasks.filter((t) => live(t.status)),
    findings,
    definition,
    // Humans review proposals; open ones wait on them.
    reviews: a.kind === "human" ? proposals.filter((p) => p.status === "open") : [],
  };
}

/** A person or a robot, for telling the two kinds apart at a glance. */
export function KindIcon({ kind }: { kind: Agent["kind"] }) {
  return kind === "human" ? (
    <svg className="kind-icon human" viewBox="0 0 24 24" aria-hidden>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21c0-4.4 3.6-8 8-8s8 3.6 8 8" />
    </svg>
  ) : (
    <svg className="kind-icon ai" viewBox="0 0 24 24" aria-hidden>
      <rect x="4" y="7" width="16" height="13" rx="3" />
      <path d="M12 3v4M9 13h.01M15 13h.01M9 17h6" />
    </svg>
  );
}

export const kindLabel = (kind: Agent["kind"]) => (kind === "human" ? "Human" : "AI agent");

export function AgentsView({ spec, report, proposals, item, go }: ViewProps) {
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState<KindFilter>("all");
  const [busy, setBusy] = useState(false);
  const [failing, setFailing] = useState(false);

  const all = useMemo(
    () =>
      [...spec.agents]
        // People first: they review and approve what the AI agents do.
        .sort((a, b) => (a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind === "human" ? -1 : 1))
        .map((a) => ({ a, ...summarize(a, spec, report, proposals) })),
    [spec, report, proposals],
  );
  const q = query.trim().toLowerCase();
  const shown = all.filter(({ a, components, openTasks, findings }) => {
    if (kind !== "all" && a.kind !== kind) return false;
    if (busy && !openTasks.length) return false;
    if (failing && !findings.length) return false;
    if (!q) return true;
    const haystack = [a.id, a.name, a.scope, ...a.owns, ...(a.rules ?? []), ...components.flatMap((c) => [c.id, c.name])];
    return haystack.some((s) => s.toLowerCase().includes(q));
  });
  const selected = all.find((x) => x.a.id === item);
  const count = (k: Agent["kind"]) => all.filter((x) => x.a.kind === k).length;
  const filtered = q || kind !== "all" || busy || failing;

  return (
    <div className="split">
      <Section title="Agents and ownership" aside={<EditButton kind="agent" label="Add agent" />}>
        <p className="lede">
          {count("agent")} AI {count("agent") === 1 ? "agent builds" : "agents build"} Habyta, each in the files it owns;{" "}
          {count("human")} {count("human") === 1 ? "human reviews and approves" : "humans review and approve"} their work and{" "}
          {count("human") === 1 ? "owns" : "own"} the spec. Every file has exactly one owner: the most specific entry wins.
        </p>
        <div className="filters" role="search">
          <input
            type="search"
            className="search"
            placeholder="Search name, scope, path, component, rule…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search agents"
          />
          <div className="segmented" role="radiogroup" aria-label="Kind">
            {(
              [
                ["all", `All ${all.length}`],
                ["agent", `AI ${count("agent")}`],
                ["human", `Human ${count("human")}`],
              ] as const
            ).map(([k, label]) => (
              <button key={k} role="radio" aria-checked={kind === k} className={kind === k ? "active" : ""} onClick={() => setKind(k)}>
                {label}
              </button>
            ))}
          </div>
          <label className="toggle">
            <input type="checkbox" checked={busy} onChange={(e) => setBusy(e.target.checked)} />
            Has open tasks
          </label>
          <label className="toggle">
            <input type="checkbox" checked={failing} onChange={(e) => setFailing(e.target.checked)} />
            Has findings
          </label>
        </div>
        {shown.length === 0 ? (
          <Empty>
            No agents match.{" "}
            {filtered && (
              <button
                className="link"
                onClick={() => {
                  setQuery("");
                  setKind("all");
                  setBusy(false);
                  setFailing(false);
                }}
              >
                Clear the filters
              </button>
            )}
          </Empty>
        ) : (
          <ul className="agent-list">
            {shown.map(({ a, components, openTasks, findings, reviews }) => (
              <li key={a.id}>
                <button
                  className={`agent-row kind-${a.kind}${selected?.a.id === a.id ? " active" : ""}`}
                  onClick={() => go("agents", selected?.a.id === a.id ? undefined : a.id)}
                  aria-current={selected?.a.id === a.id}
                >
                  <KindIcon kind={a.kind} />
                  <span className="agent-main">
                    <span className="agent-name">
                      {a.name} <span className="agent-kind">{kindLabel(a.kind)}</span>
                    </span>
                    <span className="agent-scope">{a.scope}</span>
                  </span>
                  <span className="agent-stats">
                    {openTasks.length > 0 && <Pill tone="info">{openTasks.length} open {openTasks.length === 1 ? "task" : "tasks"}</Pill>}
                    {reviews.length > 0 && <Pill tone="warn">{reviews.length} to review</Pill>}
                    {findings.length > 0 && <Pill tone="bad">{findings.length} {findings.length === 1 ? "finding" : "findings"}</Pill>}
                    <span className="muted small">
                      {components.length > 0 && `${components.length} ${components.length === 1 ? "component" : "components"} · `}
                      {a.owns.length} {a.owns.length === 1 ? "path" : "paths"}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
        {filtered && shown.length > 0 && (
          <p className="hint">
            Showing {shown.length} of {all.length}.
          </p>
        )}
      </Section>
      <aside className="detail">
        {selected ? <AgentDetail {...selected} spec={spec} go={go} /> : <Empty>Select an agent to see its charter, work, and files.</Empty>}
      </aside>
    </div>
  );
}

function AgentDetail({
  a,
  components,
  tasks,
  openTasks,
  findings,
  definition,
  reviews,
  spec,
  go,
}: { a: Agent; spec: Spec; go: ViewProps["go"] } & ReturnType<typeof summarize>) {
  const done = tasks.length - openTasks.length;
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3 className="agent-title">
          <KindIcon kind={a.kind} />
          {a.name}
        </h3>
        <EditButton kind="agent" id={a.id} />
      </div>
      <p>
        <Pill tone={a.kind === "human" ? "warn" : "info"}>{kindLabel(a.kind)}</Pill> <Code>{a.id}</Code>
      </p>
      <p>{a.scope}</p>
      <p className="muted small">
        {a.kind === "human"
          ? "A person. Reviews and accepts proposals, and works their own tasks by hand."
          : "Runs in Claude Code from its generated definition, works the tasks assigned to it, and changes only the paths it owns."}
      </p>

      {reviews.length > 0 && (
        <>
          <h4>Waiting for review</h4>
          <ul className="plain">
            {reviews.map((p) => (
              <li key={p.id}>
                <button className="link" onClick={() => go("proposals", p.id)}>
                  {p.title}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}

      <h4>
        Tasks{" "}
        <span className="muted small">
          {openTasks.length} open{done > 0 && `, ${done} closed`}
        </span>
      </h4>
      {openTasks.length ? (
        <ul className="plain">
          {openTasks.map((t) => (
            <li key={t.id}>
              <button className="link" onClick={() => go("tasks", t.id)}>
                {t.title}
              </button>{" "}
              <StatusPill status={t.status} />
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted small">Nothing open.</p>
      )}
      {a.kind === "agent" && openTasks.length > 0 && <RunTask agent={a.id} project={spec.system.name} kept={spec.charters === "fausto"} title="Run this agent" />}

      {components.length > 0 && (
        <>
          <h4>Components</h4>
          <p className="chips">
            {components.map((c) => (
              <a key={c.id} href={`#/architecture/${c.id}`} className="chip">
                {c.name}
              </a>
            ))}
          </p>
        </>
      )}

      <h4>Owns</h4>
      <ul className="plain mono small">
        {a.owns.map((o) => (
          <li key={o}>{o}</li>
        ))}
      </ul>

      {!!a.rules?.length && (
        <>
          <h4>Rules</h4>
          <ul className="rules">
            {a.rules.map((r) => (
              <li key={r}>{r}</li>
            ))}
          </ul>
        </>
      )}

      {findings.length > 0 && (
        <>
          <h4>Findings</h4>
          <ul className="findings">
            {findings.map((f, i) => (
              <li key={i}>
                <button className="link" onClick={() => go("health", f.check)}>
                  {f.check}
                </button>
                : {f.message}
              </li>
            ))}
          </ul>
        </>
      )}

      <h4>Files</h4>
      <p className="small">
        {spec.charters === "fausto" ? (
          <>
            Charter kept in fausto <span className="muted">(as {a.file}; change it through a proposal)</span>
            {a.kind === "agent" && (
              <>
                <br />
                Definition generated by fausto: <Code>fausto agents</Code> writes it to <Code>{definition}</Code>
              </>
            )}
          </>
        ) : (
          <>
            Charter <Code>{a.file}</Code>
            {a.kind === "agent" && (
              <>
                <br />
                Definition <Code>{definition}</Code> <span className="muted">(generated)</span>
              </>
            )}
          </>
        )}
      </p>
      {spec.principles.principles.length > 0 && (
        <p className="muted small">
          Also follows the {spec.principles.principles.length} <a href="#/health">principles</a>.
        </p>
      )}
    </div>
  );
}
