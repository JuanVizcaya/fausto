import { useEffect } from "react";
import type { ViewProps } from "../App";
import { EditButton } from "../editor";
import { Code, Empty, Section, StatusPill } from "../ui";

export function HealthView({ spec, report, item }: ViewProps) {
  // A principle's checks link here; bring the selected check into view.
  useEffect(() => {
    if (item) document.getElementById(item)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [item]);
  const status = (checks: string[] = []) => {
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
      <Section title="Drift checks">
        {!report ? (
          <Empty>Checks not loaded.</Empty>
        ) : (
          <>
            <div className={report.ok ? "banner good" : "banner bad"}>
              {report.ok
                ? "The code matches the system spec."
                : `${failed} of ${report.results.length} checks found drift between the code and the spec.`}
            </div>
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
                  {r.findings.length > 0 && (
                    <ul className="findings">
                      {r.findings.map((f, i) => (
                        <li key={i}>
                          {f.component && <a href={`#/architecture/${f.component}`}>{f.component}</a>} {f.message}
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

      <Section title="Implementation principles" aside={<EditButton kind="principle" label="Add principle" />}>
        <p className="lede">
          Every agent follows these. Principles with checks are enforced by <code className="code">fausto check</code>; the
          rest are reviewed by hand.
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
                    <a href={`#/health/${c}`}>{c}</a>
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
