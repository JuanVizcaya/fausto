import type { ViewProps } from "../../App";
import { EditButton, putUpdate, useEditor } from "../../editor";
import { useDraft } from "../../draft";
import { componentFindings } from "../../api";
import type { Component, Dependency, Domain, Layer, Spec, Technology } from "../../types";
import { Code, Empty, Pill, StatusPill } from "../../ui";
import { evaluate } from "../../quality";
import { live, severity, severityLabel } from "../../risk";
import { githubTreeUrl } from "../../github";
import { codeItem } from "../code/model";
import { hasCode } from "./layout";

/** What the diagram has selected: a component, a layer, or a bounded context. */
export type Selection = { kind: "component"; c: Component } | { kind: "layer"; layer: Layer } | { kind: "context"; domain: Domain };

/** Reads the selection from the route item: <component>, layer:<id>, or
 * context:<id>. Ids can't contain ":", so the prefixes don't collide. */
export function parseSelection(spec: Spec, item?: string): Selection | undefined {
  if (!item) return undefined;
  if (item.startsWith("layer:")) {
    const layer = spec.system.layers.find((l) => l.id === item.slice(6));
    return layer && { kind: "layer", layer };
  }
  if (item.startsWith("context:")) {
    const domain = spec.domains.domains.find((d) => d.id === item.slice(8));
    return domain && { kind: "context", domain };
  }
  const c = spec.system.components.find((x) => x.id === item);
  return c && { kind: "component", c };
}

/** The ids of the components a selection covers. */
export function selectedComponents(spec: Spec, sel?: Selection): Set<string> | undefined {
  if (!sel) return undefined;
  if (sel.kind === "component") return new Set([sel.c.id]);
  if (sel.kind === "layer") return new Set(spec.system.components.filter((c) => c.layer === sel.layer.id).map((c) => c.id));
  return new Set(sel.domain.components ?? []);
}

/** The side panel: details of the selection, or an overview of the system. */
export function Inspector({
  sel,
  tech,
  spec,
  report,
  go,
  repo,
  branch,
}: { sel?: Selection; tech?: Technology; repo?: string; branch: string } & Pick<ViewProps, "spec" | "report" | "go">) {
  if (sel?.kind === "component") return <ComponentDetail c={sel.c} {...{ spec, report, go, repo, branch }} />;
  if (sel?.kind === "layer") return <LayerDetail layer={sel.layer} spec={spec} report={report} />;
  if (sel?.kind === "context") return <ContextDetail domain={sel.domain} spec={spec} />;
  if (tech) return <TechDetail tech={tech} spec={spec} />;
  return <Overview spec={spec} report={report} />;
}

/** The highlighted technology's radar entry. */
function TechDetail({ tech, spec }: { tech: Technology; spec: Spec }) {
  const users = spec.system.components.filter((c) => c.tech?.includes(tech.id));
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{tech.name}</h3>
        <span className="aside-row">
          <Pill>{tech.status}</Pill>
          <EditButton kind="technology" id={tech.id} />
        </span>
      </div>
      <p className="muted">
        <Code>{tech.id}</Code> · {tech.category}
      </p>
      {tech.notes && <p>{tech.notes}</p>}
      <h4 className="sub">Used by</h4>
      {users.length ? <ComponentList components={users} /> : <Empty>Not used by any component.</Empty>}
      <p className="muted">
        See it on the <a href={`#/architecture/~technology/${tech.id}`}>technology radar</a>.
      </p>
    </div>
  );
}

/** "May depend on: a, b", with each layer linked. */
function LayerRule({ layer }: { layer: Layer }) {
  if (!layer.may_depend_on.length) return <>none</>;
  return (
    <>
      {layer.may_depend_on.map((id, i) => (
        <span key={id}>
          {i > 0 && ", "}
          <a href={`#/architecture/layer:${id}`}>{id}</a>
        </span>
      ))}
    </>
  );
}

/** A linked list of components, with their kind and status. */
function ComponentList({ components }: { components: Component[] }) {
  return (
    <ul className="plain">
      {components.map((c) => (
        <li key={c.id}>
          <a href={`#/architecture/${c.id}`}>{c.name}</a> <span className="muted">{c.kind}</span>
          {c.status !== "active" && <> <StatusPill status={c.status} /></>}
        </li>
      ))}
    </ul>
  );
}

function LayerDetail({ layer, spec, report }: { layer: Layer } & Pick<ViewProps, "spec" | "report">) {
  const members = spec.system.components.filter((c) => c.layer === layer.id);
  const dependents = spec.system.layers.filter((l) => l.may_depend_on.includes(layer.id));
  const ids = new Set(members.map((c) => c.id));
  const findings = report?.results.find((r) => r.check === "layers")?.findings.filter((f) => f.component && ids.has(f.component)) ?? [];
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{layer.id}</h3>
        <span className="aside-row">
          <Pill>layer</Pill>
          <EditButton kind="layer" id={layer.id} />
        </span>
      </div>
      {layer.description && <p>{layer.description}</p>}
      <dl className="facts">
        <dt>May depend on</dt>
        <dd>
          <LayerRule layer={layer} />
        </dd>
        <dt>Used by</dt>
        <dd>
          {dependents.length
            ? dependents.map((l, i) => (
                <span key={l.id}>
                  {i > 0 && ", "}
                  <a href={`#/architecture/layer:${l.id}`}>{l.id}</a>
                </span>
              ))
            : "no layer"}
        </dd>
      </dl>
      <h4 className="sub">Components</h4>
      {members.length ? <ComponentList components={members} /> : <Empty>No components in this layer.</Empty>}
      <h4 className="sub">Layer rules</h4>
      {!report ? (
        <Empty>Checks not loaded.</Empty>
      ) : findings.length === 0 ? (
        <p>
          <Pill tone="good">no findings</Pill>
        </p>
      ) : (
        <ul className="findings">
          {findings.map((f, i) => (
            <li key={i}>
              <a href={`#/architecture/${f.component}`}>{f.component}</a> {f.message}
              {f.path && <div className="muted"><Code>{f.path}</Code></div>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ContextDetail({ domain, spec }: { domain: Domain; spec: Spec }) {
  const members = spec.system.components.filter((c) => domain.components?.includes(c.id));
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{domain.name}</h3>
        <span className="aside-row">
          <StatusPill status={domain.status} />
          <EditButton kind="domain" id={domain.id} />
        </span>
      </div>
      <p className="muted">
        <Code>{domain.id}</Code> · bounded context
      </p>
      {domain.description && <p>{domain.description}</p>}
      <h4 className="sub">Components</h4>
      {members.length ? <ComponentList components={members} /> : <Empty>No components yet.</Empty>}
      <p className="muted">
        Its shared vocabulary is in the <a href="#/architecture/~glossary">glossary</a>.
      </p>
    </div>
  );
}

/** Counts as "3 services, 1 database". */
function tally(values: string[], plural: (v: string) => string = (v) => v + "s") {
  const counts = new Map<string, number>();
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1);
  return [...counts].map(([v, n]) => `${n} ${n === 1 ? v : plural(v)}`).join(", ");
}

function Overview({ spec, report }: Pick<ViewProps, "spec" | "report">) {
  const { components, layers } = spec.system;
  const withFindings = report ? components.filter((c) => componentFindings(report, c.id).length > 0).length : 0;
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{spec.system.name}</h3>
      </div>
      {spec.system.description && <p>{spec.system.description}</p>}
      <dl className="facts">
        <dt>Components</dt>
        <dd>{tally(components.map((c) => c.kind), (v) => (v.endsWith("y") ? v.slice(0, -1) + "ies" : v + "s"))}</dd>
        <dt>Status</dt>
        <dd>{tally(components.map((c) => c.status), (v) => v)}</dd>
        {report && (
          <>
            <dt>Health</dt>
            <dd>
              {withFindings ? (
                <a href="#/health">{withFindings === 1 ? "1 component has findings" : `${withFindings} components have findings`}</a>
              ) : (
                "no findings"
              )}
            </dd>
          </>
        )}
      </dl>
      <h4 className="sub">Layers</h4>
      <ul className="plain">
        {layers.map((l) => (
          <li key={l.id}>
            <a href={`#/architecture/layer:${l.id}`}>{l.id}</a> <span className="muted">→ {l.may_depend_on.join(", ") || "none"}</span>
          </li>
        ))}
      </ul>
      <h4 className="sub">Bounded contexts</h4>
      {spec.domains.domains.length ? (
        <ul className="plain">
          {spec.domains.domains.map((d) => (
            <li key={d.id}>
              <a href={`#/architecture/context:${d.id}`}>{d.name}</a> <StatusPill status={d.status} />
            </li>
          ))}
        </ul>
      ) : (
        <Empty>No bounded contexts declared.</Empty>
      )}
      <h4 className="sub">Reading the diagram</h4>
      <ul className="plain legend">
        <li>Rows are layers; → lists the layers each may depend on.</li>
        <li>Arrows are dependencies, labelled with their protocol; event arrows run from producer to consumer.</li>
        <li>Dashed boxes are planned components.</li>
        <li>Coloured dashed outlines are bounded contexts; the small tags beside them are contexts with no components yet.</li>
        <li>✓ or a number: the component's drift-check findings.</li>
        <li>⤵ drills into the component's code.</li>
      </ul>
    </div>
  );
}

function ComponentDetail({
  c,
  spec,
  report,
  go,
  repo,
  branch,
}: { c: Component; repo?: string; branch: string } & Pick<ViewProps, "spec" | "report" | "go">) {
  const findings = componentFindings(report, c.id);
  const dependents = spec.system.components.filter((o) => o.depends_on?.some((d) => d.target === c.id));
  const owner = spec.agents.find((a) => a.id === c.owner);
  const domains = spec.domains.domains.filter((d) => d.components?.includes(c.id));
  const tech = (id: string) => spec.tech.technologies.find((t) => t.id === id);
  const decisions = spec.decisions.filter((d) => d.affects?.includes(c.id));
  const publishes = spec.events.events.filter((e) => e.producer === c.id);
  const consumes = spec.events.events.filter((e) => e.consumers?.includes(c.id));
  const milestones = spec.roadmap.milestones.filter((m) => m.components?.includes(c.id));
  const risks = spec.risks.risks.filter((r) => live(r) && r.components?.includes(c.id));
  const requirements = spec.requirements.requirements
    .map((r) => ({ r, ...evaluate(report, r, c) }))
    .filter((x) => x.verdict !== "n/a");
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{c.name}</h3>
        <span className="aside-row">
          <StatusPill status={c.status} />
          <EditButton kind="component" id={c.id} />
        </span>
      </div>
      {hasCode(c) && (
        <button className="btn btn-primary btn-small open-code" onClick={() => go("architecture", codeItem(c.id))}>
          Open the code ›
        </button>
      )}
      {c.kind === "service" && <AddDatabase service={c} spec={spec} />}
      <p className="muted">
        <Code>{c.id}</Code> · {c.kind} in the <a href={`#/architecture/layer:${c.layer}`}>{c.layer}</a> layer
      </p>
      {c.description && <p>{c.description}</p>}

      <dl className="facts">
        {domains.length > 0 && (
          <>
            <dt>Domain</dt>
            <dd>
              {domains.map((d, i) => (
                <span key={d.id}>
                  {i > 0 && ", "}
                  <a href={`#/architecture/context:${d.id}`} title={d.description}>
                    {d.name}
                  </a>
                </span>
              ))}
            </dd>
          </>
        )}
        <dt>Owner</dt>
        <dd>
          <a href={`#/agents/${c.owner}`}>{owner?.name ?? c.owner}</a>
        </dd>
        {!!c.tech?.length && (
          <>
            <dt>Technology</dt>
            <dd className="chips">
              {c.tech.map((t) => (
                <a key={t} href={`#/architecture/~technology/${t}`} className="chip" title={tech(t) && [tech(t)!.status, tech(t)!.notes].filter(Boolean).join(": ")}>
                  {tech(t)?.name ?? t}
                </a>
              ))}
            </dd>
          </>
        )}
        {!!c.paths?.length && (
          <>
            <dt>Code</dt>
            <dd>
              {c.paths.map((p) => (
                <div key={p}>
                  <Code>{p}</Code>
                  {repo && (
                    <>
                      {" "}
                      <a href={githubTreeUrl(repo, branch, p)} target="_blank" rel="noreferrer" className="link small">
                        View on GitHub ↗
                      </a>
                    </>
                  )}
                </div>
              ))}
            </dd>
          </>
        )}
        {(c.runtime?.cmd || c.runtime?.compose || c.runtime?.host || c.runtime?.image) && (
          <>
            <dt>Runtime</dt>
            <dd>
              {c.runtime.host && <div>runs in <a href={`#/architecture/${c.runtime.host}`}>{c.runtime.host}</a></div>}
              {c.runtime.cmd && <div>command <Code>{c.runtime.cmd}</Code></div>}
              {c.runtime.image && <div>image <Code>{c.runtime.image}</Code></div>}
              {c.runtime.compose && <div>compose service <Code>{c.runtime.compose}</Code></div>}
            </dd>
          </>
        )}
        {!!c.contracts?.length && (
          <>
            <dt>Contracts</dt>
            <dd>
              {c.contracts.map((ct) => (
                <div key={ct.proto ?? ct.go}>
                  <Code>{ct.proto ?? ct.go}</Code>
                  {ct.go && <span className="muted"> Go</span>}
                </div>
              ))}
            </dd>
          </>
        )}
        {!!c.data?.length && (
          <>
            <dt>Data</dt>
            <dd>
              {c.data.map((t) => (
                <div key={t.table}>
                  <Code>{t.table}</Code>
                  {!!t.pii?.length && <span className="pii"> PII: {t.pii.join(", ")}</span>}
                </div>
              ))}
            </dd>
          </>
        )}
        {!!c.depends_on?.length && (
          <>
            <dt>Depends on</dt>
            <dd>
              {c.depends_on.map((d) => (
                <div key={d.target}>
                  <a href={`#/architecture/${d.target}`}>{d.target}</a> <span className="muted">via {d.protocol}{d.schema && `, schema ${d.schema}`}</span>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!publishes.length && (
          <>
            <dt>Publishes</dt>
            <dd>
              {publishes.map((e) => (
                <div key={e.id}>
                  <a href={`#/events/${e.id}`}>{e.title}</a> <span className="muted">{e.status}</span>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!consumes.length && (
          <>
            <dt>Consumes</dt>
            <dd>
              {consumes.map((e) => (
                <div key={e.id}>
                  <a href={`#/events/${e.id}`}>{e.title}</a> <span className="muted">from {e.producer}</span>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!dependents.length && (
          <>
            <dt>Used by</dt>
            <dd>
              {dependents.map((o) => (
                <div key={o.id}>
                  <a href={`#/architecture/${o.id}`}>{o.id}</a>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!requirements.length && (
          <>
            <dt>Quality</dt>
            <dd>
              {requirements.map(({ r, verdict }) => (
                <div key={r.id}>
                  <span className={`q-badge q-${verdict}`}>{verdict === "pass" ? "✓" : verdict === "fail" ? "✗" : "○"}</span>{" "}
                  <a href={`#/quality/${r.id}`}>{r.title}</a>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!risks.length && (
          <>
            <dt>Risks</dt>
            <dd>
              {risks.map((r) => (
                <div key={r.id}>
                  <span className={`sev-badge sev-${severityLabel(severity(r))}`}>{severity(r)}</span> <a href={`#/risks/${r.id}`}>{r.title}</a>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!milestones.length && (
          <>
            <dt>Milestones</dt>
            <dd>
              {milestones.map((m) => (
                <div key={m.id}>
                  <a href={`#/roadmap/${m.id}`}>{m.title}</a> <span className="muted">{m.status}</span>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!decisions.length && (
          <>
            <dt>Decisions</dt>
            <dd>
              {decisions.map((d) => (
                <div key={d.id}>
                  <a href={`#/decisions/${d.id}`}>
                    {d.id} {d.title}
                  </a>
                </div>
              ))}
            </dd>
          </>
        )}
      </dl>

      <h4 className="sub">Health</h4>
      {!report ? (
        <Empty>Checks not loaded.</Empty>
      ) : findings.length === 0 ? (
        <p>
          <Pill tone="good">no findings</Pill>
        </p>
      ) : (
        <ul className="findings">
          {findings.map((f, i) => (
            <li key={i}>
              <button className="link" onClick={() => go("health", f.check)}>
                {f.check}
              </button>{" "}
              {f.message}
              {f.path && <div className="muted"><Code>{f.path}</Code></div>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Adds a database owned by service to the draft, and the service's sql
 * dependency on it once the database is saved. */
function AddDatabase({ service, spec }: { service: Component; spec: Spec }) {
  const { editable, edit } = useEditor();
  const draft = useDraft();
  if (!editable) return null;
  const dataLayer = spec.system.layers.find((l) => l.id === "data")?.id ?? service.layer;
  let id = `${service.id}-db`;
  for (let n = 2; spec.system.components.some((c) => c.id === id); n++) id = `${service.id}-db-${n}`;
  const add = () =>
    edit({
      kind: "component",
      initial: {
        id,
        name: `${service.name} database`,
        description: `Database owned by the ${service.name.toLowerCase()}.`,
        kind: "database",
        layer: dataLayer,
        status: "planned",
        owner: service.owner,
        tech: ["postgres"],
      },
      onSaved: (db) => {
        const target = String(db.id ?? "");
        if (!target) return;
        putUpdate(draft, spec, "component", service.id, (v) => {
          const deps = (v.depends_on as Dependency[] | undefined) ?? [];
          return deps.some((d) => d.target === target) ? v : { ...v, depends_on: [...deps, { target, protocol: "sql" }] };
        });
      },
    });
  return (
    <p>
      <button className="btn btn-small" onClick={add}>
        Add database
      </button>
    </p>
  );
}
