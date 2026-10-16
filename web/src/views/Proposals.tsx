import { useEffect, useMemo, useState } from "react";
import type { ViewProps } from "../App";
import { approveProposal, fetchProposal, jiraProblem, rejectProposal } from "../api";
import type { Proposal, ProposalDetail } from "../types";
import { Code, Empty, Markdown, Section, StatusPill } from "../ui";

type StatusFilter = Proposal["status"] | "all";

/** "Name <email>" → "Name". */
const authorName = (author: string) => author.replace(/\s*<[^>]*>\s*$/, "") || author;

export function ProposalsView({ spec, proposals, item, go, reload }: ViewProps) {
  const anyOpen = proposals.some((p) => p.status === "open");
  const [query, setQuery] = useState("");
  // Open proposals are what needs attention; without any, show them all.
  const [status, setStatus] = useState<StatusFilter>(anyOpen ? "open" : "all");
  const [milestone, setMilestone] = useState("");

  const sorted = useMemo(
    () =>
      [...proposals].sort(
        (a, b) => Number(b.status === "open") - Number(a.status === "open") || (b.accepted ?? b.created).localeCompare(a.accepted ?? a.created),
      ),
    [proposals],
  );
  const milestoneOf = (id?: string) => spec.roadmap.milestones.find((m) => m.id === id);
  const q = query.trim().toLowerCase();
  const shown = sorted.filter((p) => {
    if ((status !== "all" && p.status !== status) || (milestone && p.milestone !== milestone)) return false;
    if (!q) return true;
    return [p.id, p.title, p.description, p.author, p.milestone ?? "", milestoneOf(p.milestone)?.title ?? ""].some((s) =>
      s.toLowerCase().includes(q),
    );
  });
  const selected = sorted.find((p) => p.id === item);
  const filtered = q || status !== (anyOpen ? "open" : "all") || milestone;
  const clear = () => {
    setQuery("");
    setStatus("all");
    setMilestone("");
  };
  const count = (s: StatusFilter) => (s === "all" ? proposals.length : proposals.filter((p) => p.status === s).length);
  const milestones = spec.roadmap.milestones.filter((m) => proposals.some((p) => p.milestone === m.id));

  return (
    <div className="split split-tasks">
      <Section title="Proposals">
        <p className="lede">
          Changes to <Code>system/</Code> are proposed here and kept in fausto's database, checked against the base branch as it is now,
          and accepted or rejected by a human. Accepting one commits just the spec change and gives each affected agent a task.
        </p>
        <div className="filters" role="search">
          <input
            type="search"
            className="search"
            placeholder="Search title, description, author, milestone…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search proposals"
          />
          <div className="segmented" role="radiogroup" aria-label="Status">
            {(
              [
                ["open", "Open"],
                ["accepted", "Accepted"],
                ["rejected", "Rejected"],
                ["all", "All"],
              ] as const
            ).map(([k, label]) => (
              <button key={k} role="radio" aria-checked={status === k} className={status === k ? "active" : ""} onClick={() => setStatus(k)}>
                {label} {count(k)}
              </button>
            ))}
          </div>
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
        </div>
        {proposals.length === 0 ? (
          <Empty>No proposals yet. Edit something in any view, then use “Review and propose” at the bottom.</Empty>
        ) : shown.length === 0 ? (
          <Empty>
            No proposals match.{" "}
            <button className="link" onClick={clear}>
              Show all
            </button>
          </Empty>
        ) : (
          <ul className="agent-list">
            {shown.map((p) => {
              const isSel = selected?.id === p.id;
              const m = milestoneOf(p.milestone);
              return (
                <li key={p.id}>
                  <button
                    className={`agent-row proposal-row status-${p.status}${isSel ? " active" : ""}`}
                    onClick={() => go("proposals", isSel ? undefined : p.id)}
                    aria-current={isSel}
                  >
                    <span className="agent-main">
                      <span className="agent-name">{p.title}</span>
                      <span className="agent-scope">
                        {authorName(p.author)} · {p.status === "accepted" && p.accepted ? `accepted ${p.accepted}` : p.created}
                        {m && ` · ${m.title}`}
                        {p.status === "accepted" && ` · ${p.tasks.length} ${p.tasks.length === 1 ? "task" : "tasks"}`}
                      </span>
                    </span>
                    <span className="agent-stats">
                      <StatusPill status={p.status} />
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {filtered && shown.length > 0 && (
          <p className="hint">
            Showing {shown.length} of {proposals.length}.{" "}
            <button className="link" onClick={clear}>
              Show all
            </button>
          </p>
        )}
      </Section>
      <aside className="detail">
        {selected ? (
          <ProposalDetailView key={selected.id} id={selected.id} {...{ spec, go, reload }} />
        ) : (
          <Empty>{anyOpen ? "Select a proposal to review it." : "Select a proposal to see what it changed."}</Empty>
        )}
      </aside>
    </div>
  );
}

function ProposalDetailView({ id, spec, go, reload }: { id: string } & Pick<ViewProps, "spec" | "go" | "reload">) {
  const [d, setD] = useState<ProposalDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<"approve" | "reject" | null>(null);
  const [busy, setBusy] = useState(false);
  const [jiraWarning, setJiraWarning] = useState<string | null>(null);

  const load = () =>
    fetchProposal(id)
      .then((x) => {
        setD(x);
        setError(null);
      })
      .catch((e) => setError(e.message));
  useEffect(() => {
    load();
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps

  const act = async (action: "approve" | "reject") => {
    setBusy(true);
    try {
      if (action === "approve") setJiraWarning(jiraProblem((await approveProposal(id)).jiraSync));
      else await rejectProposal(id);
      setConfirm(null);
      await reload();
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (error && !d) return <div className="banner bad">{error}</div>;
  if (!d) return <p className="empty">Loading proposal…</p>;
  const agentName = (a: string) => spec.agents.find((x) => x.id === a)?.name ?? a;
  const failing = d.report?.results.filter((r) => r.status === "fail") ?? [];

  return (
    <div className="detail-body proposal">
      <div className="detail-head">
        <h3>{d.title}</h3>
        <StatusPill status={d.status} />
      </div>
      <p className="muted small">
        {authorName(d.author)} · {d.created}
        {d.status === "open" && d.baseCommit && (
          <>
            {" "}· made against <Code>{d.baseCommit.slice(0, 12)}</Code>
          </>
        )}
        {d.commit && (
          <>
            {" "}· applied in <Code>{d.commit.slice(0, 12)}</Code>
          </>
        )}
        {d.legacy && " · imported from git"}
        {d.accepted && ` · accepted ${d.accepted}`}
        {d.milestone && (
          <>
            {" "}· milestone{" "}
            <a href={`#/roadmap/${d.milestone}`}>{spec.roadmap.milestones.find((m) => m.id === d.milestone)?.title ?? d.milestone}</a>
          </>
        )}
      </p>
      {d.description && <p>{d.description}</p>}
      {error && <div className="banner bad">{error}</div>}
      {jiraWarning && (
        <div className="banner warn">
          Accepted, but its tasks are not all in Jira yet ({jiraWarning}). Retry with Sync with Jira on the{" "}
          <a href="#/tasks">Tasks</a> tab.
        </div>
      )}

      {d.status === "open" && (
        <div className="actions">
          {d.conflict ? (
            <div className="banner bad">{d.conflict}</div>
          ) : confirm === "approve" ? (
            <>
              <span>
                Merge into <Code>{d.base}</Code> and create {(d.plans ?? []).length} {(d.plans ?? []).length === 1 ? "task" : "tasks"}?
              </span>
              <button className="btn btn-primary" disabled={busy} onClick={() => act("approve")}>
                {busy ? "Merging…" : "Confirm accept"}
              </button>
              <button className="btn" onClick={() => setConfirm(null)}>
                Cancel
              </button>
            </>
          ) : confirm === "reject" ? (
            <>
              <span>
                Reject? It stays listed as rejected.
              </span>
              <button className="btn btn-danger" disabled={busy} onClick={() => act("reject")}>
                Confirm reject
              </button>
              <button className="btn" onClick={() => setConfirm(null)}>
                Cancel
              </button>
            </>
          ) : (
            <>
              <button className="btn btn-primary" onClick={() => setConfirm("approve")}>
                Accept…
              </button>
              <button className="btn" onClick={() => setConfirm("reject")}>
                Reject…
              </button>
            </>
          )}
        </div>
      )}

      {d.status === "accepted" ? (
        <>
          <h3 className="sub">Tasks created</h3>
          {d.tasks.length === 0 ? (
            <Empty>No agent was affected.</Empty>
          ) : (
            <ul className="plain">
              {d.tasks.map((t) => (
                <li key={t}>
                  <button className="link" onClick={() => go("tasks", t)}>
                    {t}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </>
      ) : (
        <>
          <h3 className="sub">Spec changes</h3>
          {d.changes.length === 0 ? (
            <Empty>No changes to spec items.</Empty>
          ) : (
            <ul className="changes">
              {d.changes.map((c) => (
                <li key={c.kind + c.id}>
                  <span className={`pill pill-${c.action === "added" ? "good" : c.action === "removed" ? "bad" : "warn"}`}>{c.action}</span>{" "}
                  {c.kind} <Code>{c.id}</Code>
                  {c.name && c.name !== c.id && <span className="muted"> {c.name}</span>}
                  {c.fields && <span className="muted"> · {c.fields.join(", ")}</span>}
                </li>
              ))}
            </ul>
          )}

          <h3 className="sub">{d.status === "open" ? "Tasks on accept" : "Tasks it would have created"}</h3>
          {(d.plans ?? []).length === 0 ? (
            <Empty>No agent is affected.</Empty>
          ) : (
            <div className="plans">
              {(d.plans ?? []).map((p) => (
                <div key={p.id} className="plan">
                  <div className="card-head">
                    <a href={`#/agents/${p.agent}`}>
                      <strong>{agentName(p.agent)}</strong>
                    </a>
                    <span className="chips">
                      {p.components.map((c) => (
                        <a key={c} className="chip" href={`#/architecture/${c}`}>
                          {c}
                        </a>
                      ))}
                    </span>
                  </div>
                  <ul>
                    {p.specChanges.map((c, i) => (
                      <li key={i}>
                        <Markdown source={c} inline />
                      </li>
                    ))}
                  </ul>
                  {p.findings.length > 0 && (
                    <ul className="findings">
                      {p.findings.map((f, i) => (
                        <li key={i}>
                          <Markdown source={f} inline />
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              ))}
            </div>
          )}
          {(d.unassigned ?? []).length > 0 && (
            <>
              <h3 className="sub">Findings with no agent owner</h3>
              <ul className="findings">
                {(d.unassigned ?? []).map((f, i) => (
                  <li key={i}>
                    <Markdown source={f} inline />
                  </li>
                ))}
              </ul>
            </>
          )}

          <h3 className="sub">Checks after merge</h3>
          {!d.report ? (
            <Empty>Not available.</Empty>
          ) : (
            <p className="chips">
              {d.report.results.map((r) => (
                <span key={r.check} className={`pill pill-${r.status === "pass" ? "good" : r.status === "fail" ? "bad" : "muted"}`}>
                  {r.check}
                </span>
              ))}
              {failing.length > 0 && <span className="muted"> Failures become tasks for the owning agents.</span>}
            </p>
          )}

          <h3 className="sub">Diff ({d.files.length} {d.files.length === 1 ? "file" : "files"})</h3>
          <Diff text={d.diff} />
        </>
      )}
    </div>
  );
}

function Diff({ text }: { text: string }) {
  if (!text) return <Empty>No differences.</Empty>;
  return (
    <pre className="diff">
      {text.split("\n").map((line, i) => {
        const cls = line.startsWith("+++") || line.startsWith("---") || line.startsWith("diff ") || line.startsWith("index ")
          ? "diff-meta"
          : line.startsWith("@@")
            ? "diff-hunk"
            : line.startsWith("+")
              ? "diff-add"
              : line.startsWith("-")
                ? "diff-del"
                : "";
        return (
          <span key={i} className={cls}>
            {line + "\n"}
          </span>
        );
      })}
    </pre>
  );
}
