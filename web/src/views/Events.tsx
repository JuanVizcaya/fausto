import type { ViewProps } from "../App";
import { EditButton } from "../editor";
import type { EventContract } from "../types";
import { Code, Empty, Section, StatusPill } from "../ui";

const order = { active: 0, planned: 1, deprecated: 2 } as const;

export function EventsView({ spec, report, item, go }: ViewProps) {
  const events = [...spec.events.events].sort((a, b) => order[a.status] - order[b.status] || a.id.localeCompare(b.id));
  const selected = events.find((e) => e.id === item);
  const findings = (e: EventContract) =>
    (report?.results ?? []).flatMap((r) => r.findings.filter((f) => f.message.includes(`"${e.id}"`)).map((f) => ({ ...f, check: r.check })));

  return (
    <div className="split">
      <Section title="Events" aside={<EditButton kind="event" label="Add event" />}>
        <p className="lede">
          Asynchronous messages between services. Each event has one producer and a protobuf message in a versioned
          package; consumers of at-least-once events must handle duplicates.
        </p>
        {events.length === 0 ? (
          <Empty>No events declared.</Empty>
        ) : (
          <div className="risk-list">
            {events.map((e) => (
              <button
                key={e.id}
                className={`risk-row event-row${selected?.id === e.id ? " selected" : ""}${e.status === "deprecated" ? " dim" : ""}`}
                onClick={() => go("events", selected?.id === e.id ? undefined : e.id)}
              >
                <span className="event-main">
                  <span className="risk-title">{e.title}</span>
                  <span className="muted small mono">{e.message}</span>
                </span>
                <span className="event-flow small">
                  <strong>{e.producer}</strong> → {e.consumers?.length ? e.consumers.join(", ") : <span className="muted">no consumers yet</span>}
                </span>
                {findings(e).length > 0 && <span className="pill pill-bad">{findings(e).length} findings</span>}
                <StatusPill status={e.status} />
              </button>
            ))}
          </div>
        )}
      </Section>
      <aside className="detail">
        {selected ? (
          <div className="detail-body">
            <div className="detail-head">
              <h3>{selected.title}</h3>
              <span className="aside-row">
                <StatusPill status={selected.status} />
                <EditButton kind="event" id={selected.id} />
              </span>
            </div>
            <p className="muted">
              <Code>{selected.id}</Code>
            </p>
            <p>{selected.description}</p>
            <dl className="facts">
              <dt>Message</dt>
              <dd>
                <Code>{selected.message}</Code>
              </dd>
              <dt>Producer</dt>
              <dd>
                <a href={`#/architecture/${selected.producer}`}>{selected.producer}</a>
              </dd>
              <dt>Consumers</dt>
              <dd>
                {selected.consumers?.length
                  ? selected.consumers.map((c) => (
                      <div key={c}>
                        <a href={`#/architecture/${c}`}>{c}</a>
                      </div>
                    ))
                  : "none yet"}
              </dd>
              {selected.key && (
                <>
                  <dt>Key</dt>
                  <dd>
                    <Code>{selected.key}</Code> <span className="muted small">orders and partitions messages</span>
                  </dd>
                </>
              )}
              {selected.delivery && (
                <>
                  <dt>Delivery</dt>
                  <dd>{selected.delivery}</dd>
                </>
              )}
            </dl>
            {findings(selected).length > 0 && (
              <ul className="findings">
                {findings(selected).map((f, i) => (
                  <li key={i}>
                    <strong>{f.check}</strong> {f.message}
                  </li>
                ))}
              </ul>
            )}
          </div>
        ) : (
          <Empty>Select an event to see its contract.</Empty>
        )}
      </aside>
    </div>
  );
}
