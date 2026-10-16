import type { ViewProps } from "../App";
import { EditButton } from "../editor";
import { applies, evaluate, type Verdict } from "../quality";
import type { Requirement } from "../types";
import { Code, Empty, Section } from "../ui";

const categories: Requirement["category"][] = ["security", "availability", "performance", "durability", "privacy", "operability"];

const symbol: Record<Verdict, string> = {
  pass: "✓",
  fail: "✗",
  manual: "○",
  planned: "–",
  unknown: "?",
  "n/a": "",
};

const label: Record<Verdict, string> = {
  pass: "met (checked)",
  fail: "failing",
  manual: "reviewed by hand",
  planned: "component is planned",
  unknown: "check did not run",
  "n/a": "does not apply",
};

export function QualityView({ spec, report, item, go }: ViewProps) {
  const reqs = spec.requirements.requirements;
  const components = spec.system.components.filter((c) => c.status !== "deprecated");
  const selected = reqs.find((r) => r.id === item);

  const cells = reqs.flatMap((r) => components.map((c) => evaluate(report, r, c).verdict));
  const count = (v: Verdict) => cells.filter((x) => x === v).length;

  return (
    <div className="split">
      <Section title="Quality requirements" aside={<EditButton kind="requirement" label="Add requirement" />}>
        <p className="lede">
          Non-functional requirements by component. Requirements with a check are verified on every refresh; the rest are
          reviewed by hand. {count("pass")} checks met · {count("fail")} failing · {count("manual")} reviewed by hand.
        </p>
        {reqs.length === 0 ? (
          <Empty>No requirements yet.</Empty>
        ) : (
          <div className="table-wrap">
            <table className="quality">
              <thead>
                <tr>
                  <th className="q-req">Requirement</th>
                  {components.map((c) => (
                    <th key={c.id} className="q-col">
                      <a href={`#/architecture/${c.id}`}>{c.id}</a>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {categories
                  .filter((cat) => reqs.some((r) => r.category === cat))
                  .flatMap((cat) => [
                    <tr key={cat} className="q-cat">
                      <th colSpan={components.length + 1}>{cat}</th>
                    </tr>,
                    ...reqs
                      .filter((r) => r.category === cat)
                      .map((r) => (
                        <tr key={r.id} className={selected?.id === r.id ? "selected" : ""}>
                          <th className="q-req">
                            <button className="link" onClick={() => go("quality", selected?.id === r.id ? undefined : r.id)}>
                              {r.title}
                            </button>
                            <span className="muted small">{r.target ?? (r.verified_by ? r.verified_by : "manual")}</span>
                          </th>
                          {components.map((c) => {
                            const { verdict, findings } = evaluate(report, r, c);
                            return (
                              <td key={c.id} className={`q-cell q-${verdict}`} title={`${c.id}: ${label[verdict]}${findings.length ? "\n" + findings.join("\n") : ""}`}>
                                <span aria-label={label[verdict]}>{symbol[verdict]}</span>
                              </td>
                            );
                          })}
                        </tr>
                      )),
                  ])}
              </tbody>
            </table>
          </div>
        )}
        <p className="hint">
          ✓ met · ✗ failing · ○ reviewed by hand · – planned component · blank: does not apply. Click a requirement for
          details.
        </p>
      </Section>
      <aside className="detail">
        {selected ? <RequirementDetail r={selected} {...{ spec, report }} /> : <Empty>Select a requirement to see where it applies and how it is verified.</Empty>}
      </aside>
    </div>
  );
}

function RequirementDetail({ r, spec, report }: { r: Requirement } & Pick<ViewProps, "spec" | "report">) {
  const targets = spec.system.components.filter((c) => applies(r, c));
  const a = r.applies_to ?? {};
  const scope = [
    a.kinds?.length && `kinds ${a.kinds.join(", ")}`,
    a.layers?.length && `layers ${a.layers.join(", ")}`,
    a.components?.length && `components ${a.components.join(", ")}`,
  ].filter(Boolean);
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{r.title}</h3>
        <EditButton kind="requirement" id={r.id} />
      </div>
      <p className="muted">
        <Code>{r.id}</Code> · {r.category}
      </p>
      <p>{r.statement}</p>
      <dl className="facts">
        {r.target && (
          <>
            <dt>Target</dt>
            <dd>{r.target}</dd>
          </>
        )}
        <dt>Applies to</dt>
        <dd>{scope.length ? scope.join("; ") : "every component"}</dd>
        <dt>Verified by</dt>
        <dd>{r.verified_by ? <Code>{r.verified_by}</Code> : "reviewed by hand"}</dd>
      </dl>
      <h4 className="sub">Components</h4>
      {targets.length === 0 ? (
        <Empty>Applies to no component yet.</Empty>
      ) : (
        <ul className="plain">
          {targets.map((c) => {
            const { verdict, findings } = evaluate(report, r, c);
            return (
              <li key={c.id}>
                <span className={`q-badge q-${verdict}`}>{symbol[verdict] || "·"}</span> <a href={`#/architecture/${c.id}`}>{c.id}</a>{" "}
                <span className="muted small">{label[verdict]}</span>
                {findings.length > 0 && (
                  <ul className="findings">
                    {findings.map((f, i) => (
                      <li key={i}>{f}</li>
                    ))}
                  </ul>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
