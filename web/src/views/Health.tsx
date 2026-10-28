import { useEffect, useState } from "react";
import type { ViewProps } from "../App";
import { fetchRevision, fetchRevisions, restoreRevision } from "../api";
import { EditButton } from "../editor";
import type { SpecRevision, SpecRevisionDiff } from "../types";
import { Code, Empty, Section, StatusPill } from "../ui";

export function HealthView({ spec, report, item, info, reload }: ViewProps) {
  // A principle's checks link here; bring the selected check into view.
  useEffect(() => {
    if (item) document.getElementById(item)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [item]);
  const status = (all: string[] = []) => {
    const checks = all.filter((c) => c !== "review");
    if (checks.length === 0) return "manual";
    if (!report) return "skipped";
    const results = report.results.filter((r) => checks.includes(r.check));
    if (results.some((r) => r.status === "fail")) return "fail";
    if (results.some((r) => r.status === "skipped")) return "skipped";
    return "pass";
  };
  const failed = report?.results.filter((r) => r.status === "fail").length ?? 0;
  return (
    <>
      <Section title="Drift checks" aside={<EditButton kind="check" label="Add project check" />}>
        {!report ? (
          <Empty>Checks not loaded.</Empty>
        ) : (
          <>
            <div className={report.ok ? "banner good" : "banner bad"}>
              {report.ok
                ? "The code matches the system spec."
                : `${failed} of ${report.results.length} checks found drift between the code and the spec.`}
            </div>
            {spec.source === "fausto" && (
              <p className="muted small">
                {info?.prChecks ? (
                  <>
                    fausto checks the open pull requests of <Code>{info.prChecks}</Code> against the spec, and reports on each as{" "}
                    <Code>fausto/check</Code>.
                  </>
                ) : (
                  <>Pull requests aren't checked: {info?.prChecksOff ?? "fausto isn't editable here"}.</>
                )}
              </p>
            )}
            <div className="checks">
              {report.results.map((r) => (
                <article key={r.check} id={r.check} className={item === r.check ? "card selected" : "card"}>
                  <div className="card-head">
                    <h3>
                      <Code>{r.check}</Code>
                    </h3>
                    <StatusPill status={r.status} />
                  </div>
                  <p>{r.description}</p>
                  {r.command && (
                    <p className="muted small">
                      <Code>{r.command}</Code> <EditButton kind="check" id={r.check} />
                      {r.status === "skipped" && (
                        <>
                          <br />A project check: it runs in CI and in agents' clones (<Code>fausto check -commands</Code>), never on
                          fausto's server.
                        </>
                      )}
                    </p>
                  )}
                  {r.findings.length > 0 && (
                    <ul className="findings">
                      {r.findings.map((f, i) => (
                        <li key={i}>
                          {f.component && <a href={`#/architecture/${f.component}`}>{f.component}</a>}{" "}
                          {f.message.includes("\n") ? <pre className="mono small output">{f.message}</pre> : f.message}
                          {f.path && <div className="muted"><Code>{f.path}</Code></div>}
                        </li>
                      ))}
                    </ul>
                  )}
                </article>
              ))}
            </div>
          </>
        )}
      </Section>

      {spec.source === "fausto" && <SpecHistory current={spec.revision ?? 0} editable={!!info?.editable} reload={reload} />}

      <Section title="Implementation principles" aside={<EditButton kind="principle" label="Add principle" />}>
        <p className="lede">
          Every agent follows these. Principles with checks are enforced by <code className="code">fausto check</code>: its
          built-in checks, or the project's own (their commands). Those marked <code className="code">review</code>, and those
          with no checks, are reviewed by people.
        </p>
        <ol className="principles">
          {spec.principles.principles.map((p) => (
            <li key={p.id} className="card">
              <div className="card-head">
                <h3>{p.title}</h3>
                <span className="aside-row">
                  <StatusPill status={status(p.checks)} />
                  <EditButton kind="principle" id={p.id} />
                </span>
              </div>
              <p>{p.rationale}</p>
              <p className="muted">
                <code className="code">{p.id}</code>
                {!!p.checks?.length && " · enforced by "}
                {p.checks?.map((c, i) => (
                  <span key={c}>
                    {i > 0 && ", "}
                    {c === "review" ? "review" : <a href={`#/health/${c}`}>{c}</a>}
                  </span>
                ))}
              </p>
            </li>
          ))}
        </ol>
      </Section>
    </>
  );
}

/** The spec fausto keeps, revision by revision: who changed what, with each
 * revision's diff, and a way back to an earlier one. */
function SpecHistory({ current, editable, reload }: { current: number; editable: boolean; reload: () => Promise<void> }) {
  const [revisions, setRevisions] = useState<SpecRevision[] | null>(null);
  const [open, setOpen] = useState<number | null>(null);
  const [diff, setDiff] = useState<SpecRevisionDiff | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    fetchRevisions()
      .then(setRevisions)
      .catch((e) => setError(e.message));
  }, [current]);
  useEffect(() => {
    setDiff(null);
    if (open !== null) fetchRevision(open).then(setDiff).catch((e) => setError(e.message));
  }, [open]);
  const restore = async (n: number) => {
    setBusy(true);
    try {
      await restoreRevision(n);
      setOpen(null);
      await reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Section title="Spec history">
      <p className="lede">
        fausto keeps this project's spec, as revisions: every applied proposal adds one. Open a revision to see what it changed.
      </p>
      {error && <div className="banner bad">{error}</div>}
      {!revisions ? (
        <Empty>Loading…</Empty>
      ) : (
        <ul className="agent-list revisions">
          {revisions.map((r) => (
            <li key={r.n}>
              <button className={`agent-row${open === r.n ? " active" : ""}`} onClick={() => setOpen(open === r.n ? null : r.n)} aria-expanded={open === r.n}>
                <span className="agent-main">
                  <span className="agent-name">
                    r{r.n} · {r.message}
                  </span>
                  <span className="agent-scope">
                    {r.by.replace(/\s*<[^>]*>\s*$/, "")} · {new Date(r.at).toLocaleString()}
                    {r.proposal && " · proposal " + r.proposal}
                  </span>
                </span>
                {r.n === current && <StatusPill status="active" />}
              </button>
              {open === r.n && (
                <div className="revision">
                  {r.proposal && (
                    <p className="small">
                      <a href={`#/proposals/${r.proposal}`}>Open the proposal</a>
                    </p>
                  )}
                  {!diff ? (
                    <Empty>Loading…</Empty>
                  ) : (
                    <>
                      {diff.changes.length > 0 && (
                        <ul className="changes">
                          {diff.changes.map((c) => (
                            <li key={c.kind + c.id}>
                              <span className={`pill pill-${c.action === "added" ? "good" : c.action === "removed" ? "bad" : "warn"}`}>{c.action}</span> {c.kind}{" "}
                              <Code>{c.id}</Code>
                            </li>
                          ))}
                        </ul>
                      )}
                      <pre className="diff mono">{diff.diff || "No changes."}</pre>
                    </>
                  )}
                  {editable && r.n !== current && (
                    <button className="btn" disabled={busy} onClick={() => restore(r.n)}>
                      {busy ? "Restoring…" : `Restore revision ${r.n}`}
                    </button>
                  )}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}
