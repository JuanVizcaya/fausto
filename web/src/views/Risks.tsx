import type { ViewProps } from "../App";
import { EditButton } from "../editor";
import { live, overdue, severity, severityLabel } from "../risk";
import type { Level, Risk } from "../types";
import { Code, Empty, Section, StatusPill } from "../ui";

const levels: Level[] = ["low", "medium", "high"];

export function RisksView({ spec, item, go }: ViewProps) {
  const risks = [...spec.risks.risks].sort(
    (a, b) => Number(live(b)) - Number(live(a)) || severity(b) - severity(a) || a.title.localeCompare(b.title),
  );
  const selected = risks.find((r) => r.id === item);
  const active = risks.filter(live);
  const select = (id: string) => go("risks", selected?.id === id ? undefined : id);

  return (
    <div className="split">
      <div>
        <Section title="Risk register" aside={<EditButton kind="risk" label="Add risk" />}>
          <p className="lede">
            {active.length} live {active.length === 1 ? "risk" : "risks"}
            {active.some((r) => severity(r) >= 6) && `, ${active.filter((r) => severity(r) >= 6).length} of them high or critical`}
            {risks.some((r) => overdue(r)) && `, ${risks.filter((r) => overdue(r)).length} overdue for review`}. Accepted and closed
            risks stay listed for the record.
          </p>
          <div className="heatmap" role="table" aria-label="Live risks by likelihood and impact">
            <div className="hm-axis hm-y" aria-hidden>
              impact →
            </div>
            {[...levels].reverse().map((impact) => (
              <div key={impact} className="hm-row" role="row">
                <div className="hm-label" role="rowheader">
                  {impact}
                </div>
                {levels.map((likelihood) => {
                  const score = { low: 1, medium: 2, high: 3 }[likelihood] * { low: 1, medium: 2, high: 3 }[impact];
                  const here = active.filter((r) => r.likelihood === likelihood && r.impact === impact);
                  return (
                    <div key={likelihood} role="cell" className={`hm-cell sev-${severityLabel(score)}`}>
                      {here.map((r) => (
                        <button
                          key={r.id}
                          className={selected?.id === r.id ? "hm-chip selected" : "hm-chip"}
                          onClick={() => select(r.id)}
                          title={r.title}
                        >
                          {r.title}
                        </button>
                      ))}
                    </div>
                  );
                })}
              </div>
            ))}
            <div className="hm-row hm-foot" aria-hidden>
              <div className="hm-label" />
              {levels.map((l) => (
                <div key={l} className="hm-label">
                  {l}
                </div>
              ))}
            </div>
            <div className="hm-axis hm-x" aria-hidden>
              likelihood →
            </div>
          </div>
        </Section>

        <Section title="All risks">
          {risks.length === 0 ? (
            <Empty>No risks recorded.</Empty>
          ) : (
            <div className="risk-list">
              {risks.map((r) => (
                <button key={r.id} className={`risk-row${selected?.id === r.id ? " selected" : ""}${live(r) ? "" : " dim"}`} onClick={() => select(r.id)}>
                  <span className={`sev-badge sev-${severityLabel(severity(r))}`}>{severity(r)}</span>
                  <span className="risk-title">{r.title}</span>
                  <span className="muted small">{r.category}</span>
                  {overdue(r) && <span className="pill pill-bad">review overdue</span>}
                  <StatusPill status={r.status} />
                </button>
              ))}
            </div>
          )}
        </Section>
      </div>
      <aside className="detail">
        {selected ? <RiskDetail r={selected} spec={spec} /> : <Empty>Select a risk to see what could happen and what is being done about it.</Empty>}
      </aside>
    </div>
  );
}

function RiskDetail({ r, spec }: { r: Risk } & Pick<ViewProps, "spec">) {
  const score = severity(r);
  const owner = spec.agents.find((a) => a.id === r.owner);
  const milestone = (id: string) => spec.roadmap.milestones.find((m) => m.id === id)?.title ?? id;
  const requirement = (id: string) => spec.requirements.requirements.find((x) => x.id === id)?.title ?? id;
  const decision = (id: string) => spec.decisions.find((d) => d.id === id)?.title ?? id;
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{r.title}</h3>
        <span className="aside-row">
          <StatusPill status={r.status} />
          <EditButton kind="risk" id={r.id} />
        </span>
      </div>
      <p className="muted">
        <Code>{r.id}</Code> · {r.category}
      </p>
      <p>
        <span className={`sev-badge sev-${severityLabel(score)}`}>{score}</span> {severityLabel(score)} severity: {r.likelihood}{" "}
        likelihood, {r.impact} impact
      </p>
      <p>{r.description}</p>
      <h4 className="sub">Mitigation</h4>
      {r.mitigation ? <p>{r.mitigation}</p> : <Empty>None recorded.</Empty>}
      <dl className="facts">
        <dt>Owner</dt>
        <dd>
          <a href={`#/agents/${r.owner}`}>{owner?.name ?? r.owner}</a>
        </dd>
        {r.review_by && (
          <>
            <dt>Review by</dt>
            <dd>
              {r.review_by} {overdue(r) && <span className="pill pill-bad">overdue</span>}
            </dd>
          </>
        )}
        {!!r.components?.length && (
          <>
            <dt>Components</dt>
            <dd>
              {r.components.map((id) => (
                <div key={id}>
                  <a href={`#/architecture/${id}`}>{id}</a>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!r.milestones?.length && (
          <>
            <dt>Milestones</dt>
            <dd>
              {r.milestones.map((id) => (
                <div key={id}>
                  <a href={`#/roadmap/${id}`}>{milestone(id)}</a>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!r.requirements?.length && (
          <>
            <dt>Requirements</dt>
            <dd>
              {r.requirements.map((id) => (
                <div key={id}>
                  <a href={`#/quality/${id}`}>{requirement(id)}</a>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!r.decisions?.length && (
          <>
            <dt>Decisions</dt>
            <dd>
              {r.decisions.map((id) => (
                <div key={id}>
                  <a href={`#/decisions/${id}`}>
                    {id} {decision(id)}
                  </a>
                </div>
              ))}
            </dd>
          </>
        )}
      </dl>
    </div>
  );
}
