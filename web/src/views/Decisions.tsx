import type { ViewProps } from "../App";
import { EditButton } from "../editor";
import { Code, Empty, Markdown, Section, StatusPill } from "../ui";

export function DecisionsView({ spec, item, go }: ViewProps) {
  const decisions = [...spec.decisions].sort((a, b) => b.id.localeCompare(a.id));
  const selected = decisions.find((d) => d.id === item) ?? decisions[0];
  return (
    <div className="split">
      <Section title="Decision log" aside={<EditButton kind="decision" label="New decision" />}>
        <ol className="timeline">
          {decisions.map((d) => (
            <li key={d.id} className={d.id === selected?.id ? "active" : ""}>
              <button className="timeline-item" onClick={() => go("decisions", d.id)}>
                <span className="timeline-id">{d.id}</span>
                <span className="timeline-title">{d.title}</span>
                <span className="timeline-meta">
                  {d.date} · <StatusPill status={d.status} />
                </span>
              </button>
            </li>
          ))}
        </ol>
        {decisions.length === 0 && <Empty>No decisions recorded yet.</Empty>}
      </Section>
      <aside className="detail">
        {selected ? (
          <div className="detail-body">
            <div className="detail-head">
              <h3>
                {selected.id} · {selected.title}
              </h3>
              <span className="aside-row">
                <StatusPill status={selected.status} />
                <EditButton kind="decision" id={selected.id} />
              </span>
            </div>
            <p className="muted">
              {selected.date} · <Code>{selected.file}</Code>
              {spec.source === "fausto" && <span className="muted"> (kept in fausto)</span>}
              {selected.supersedes && (
                <>
                  {" "}· supersedes <a href={`#/decisions/${selected.supersedes}`}>{selected.supersedes}</a>
                </>
              )}
            </p>
            {!!selected.affects?.length && (
              <p className="chips">
                {selected.affects.map((c) => (
                  <a key={c} href={`#/architecture/${c}`} className="chip">
                    {c}
                  </a>
                ))}
              </p>
            )}
            <Markdown source={selected.body} />
          </div>
        ) : (
          <Empty>Select a decision.</Empty>
        )}
      </aside>
    </div>
  );
}
