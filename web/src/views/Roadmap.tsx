import { useMemo } from "react";
import type { ViewProps } from "../App";
import { componentFindings } from "../api";
import { EditButton } from "../editor";
import type { Milestone, Spec, Task } from "../types";
import { Code, Empty, Section, StatusPill } from "../ui";
import { live, severity, severityLabel } from "../risk";

const NODE_W = 236;
const NODE_H = 104;
const COL_GAP = 72;
const ROW_GAP = 22;
const PAD = 20;

interface Placed {
  m: Milestone;
  x: number;
  y: number;
}

/** A milestone is blocked while it is unfinished and a dependency is too. */
export function blockedBy(spec: Spec, m: Milestone): string[] {
  if (m.status === "done" || m.status === "dropped") return [];
  return (m.depends_on ?? []).filter((id) => {
    const d = spec.roadmap.milestones.find((x) => x.id === id);
    return d && d.status !== "done" && d.status !== "dropped";
  });
}

function progress(m: Milestone) {
  const criteria = m.exit_criteria ?? [];
  return { done: criteria.filter((c) => c.done).length, total: criteria.length };
}

function milestoneTasks(spec: Spec, id: string): Task[] {
  return spec.tasks.filter((t) => t.milestone === id);
}

/** Columns by dependency depth: a milestone sits right of everything it
 * depends on. Rows keep roadmap order within a column. */
function layout(milestones: Milestone[]) {
  const byId = new Map(milestones.map((m) => [m.id, m]));
  const depth = new Map<string, number>();
  const visit = (id: string, seen: Set<string>): number => {
    if (depth.has(id)) return depth.get(id)!;
    if (seen.has(id)) return 0; // cycles are reported by the schema check
    seen.add(id);
    const deps = (byId.get(id)?.depends_on ?? []).filter((d) => byId.has(d));
    const d = deps.length ? Math.max(...deps.map((x) => visit(x, seen) + 1)) : 0;
    depth.set(id, d);
    return d;
  };
  milestones.forEach((m) => visit(m.id, new Set()));
  const columns: Milestone[][] = [];
  for (const m of milestones) (columns[depth.get(m.id)!] ??= []).push(m);
  const placed = new Map<string, Placed>();
  columns.forEach((col, c) =>
    col.forEach((m, r) => placed.set(m.id, { m, x: PAD + c * (NODE_W + COL_GAP), y: PAD + r * (NODE_H + ROW_GAP) })),
  );
  const rows = Math.max(1, ...columns.map((c) => c.length));
  return {
    placed,
    width: PAD * 2 + columns.length * NODE_W + Math.max(0, columns.length - 1) * COL_GAP,
    height: PAD * 2 + rows * NODE_H + (rows - 1) * ROW_GAP,
  };
}

const truncate = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + "…" : s);

export function RoadmapView({ spec, report, item, go }: ViewProps) {
  const milestones = spec.roadmap.milestones;
  const { placed, width, height } = useMemo(() => layout(milestones), [milestones]);
  const selected = milestones.find((m) => m.id === item);
  const counts = (["in-progress", "planned", "done", "dropped"] as const).map((s) => [s, milestones.filter((m) => m.status === s).length] as const);

  return (
    <div className="split">
      <Section title="Roadmap" aside={<EditButton kind="milestone" label="Add milestone" />}>
        <p className="lede">
          Milestones flow left to right: each one sits after the milestones it depends on. Progress counts exit criteria
          met.{" "}
          {counts
            .filter(([, n]) => n)
            .map(([s, n]) => `${n} ${s}`)
            .join(" · ")}
        </p>
        {milestones.length === 0 ? (
          <Empty>No milestones yet. Add one to start the roadmap.</Empty>
        ) : (
          <div className="diagram-wrap">
            <svg className="diagram roadmap" viewBox={`0 0 ${width} ${height}`} width={width} role="img" aria-label="Milestones by dependency">
              <defs>
                <marker id="rm-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" className="arrowhead" />
                </marker>
              </defs>
              {[...placed.values()].flatMap((p) =>
                (p.m.depends_on ?? []).map((dep) => {
                  const from = placed.get(dep);
                  if (!from) return null;
                  const sx = from.x + NODE_W;
                  const sy = from.y + NODE_H / 2;
                  const tx = p.x;
                  const ty = p.y + NODE_H / 2;
                  const mid = (sx + tx) / 2;
                  const hi = selected && (selected.id === p.m.id || selected.id === dep);
                  return (
                    <path
                      key={`${dep}-${p.m.id}`}
                      d={`M ${sx} ${sy} C ${mid} ${sy}, ${mid} ${ty}, ${tx} ${ty}`}
                      className={`edge-line${hi ? " hi" : ""}${selected && !hi ? " dim" : ""}`}
                      markerEnd="url(#rm-arrow)"
                    />
                  );
                }),
              )}
              {[...placed.values()].map(({ m, x, y }) => {
                const { done, total } = progress(m);
                const blocked = blockedBy(spec, m).length > 0;
                const isSel = selected?.id === m.id;
                return (
                  <g
                    key={m.id}
                    transform={`translate(${x} ${y})`}
                    className={`node milestone ms-${m.status}${isSel ? " selected" : ""}`}
                    onClick={() => go("roadmap", isSel ? undefined : m.id)}
                    onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && go("roadmap", m.id)}
                    tabIndex={0}
                    role="button"
                    aria-label={`${m.title}, ${m.status}${blocked ? ", blocked" : ""}`}
                  >
                    <rect width={NODE_W} height={NODE_H} rx={10} className="node-box" />
                    <rect width={5} height={NODE_H} rx={2} className="ms-stripe" />
                    <text x={16} y={26} className="node-title">
                      {truncate(m.title, 24)}
                    </text>
                    <text x={16} y={46} className="node-meta">
                      {m.status}
                      {m.target ? ` · ${m.target}` : ""}
                      {blocked ? " · blocked" : ""}
                    </text>
                    <text x={16} y={66} className="node-meta">
                      {total ? `${done}/${total} criteria` : "no exit criteria"}
                      {milestoneTasks(spec, m.id).length ? ` · ${milestoneTasks(spec, m.id).length} tasks` : ""}
                    </text>
                    <rect x={16} y={80} width={NODE_W - 32} height={6} rx={3} className="bar-track" />
                    {total > 0 && <rect x={16} y={80} width={((NODE_W - 32) * done) / total} height={6} rx={3} className="bar-fill" />}
                  </g>
                );
              })}
            </svg>
          </div>
        )}
        <p className="hint">Click a milestone for details. Arrows point from a milestone to the ones that depend on it.</p>
      </Section>
      <aside className="detail">
        {selected ? <MilestoneDetail m={selected} spec={spec} report={report} /> : <Empty>Select a milestone to see its details.</Empty>}
      </aside>
    </div>
  );
}

function MilestoneDetail({ m, spec, report }: { m: Milestone } & Pick<ViewProps, "spec" | "report">) {
  const { done, total } = progress(m);
  const blocked = blockedBy(spec, m);
  const tasks = milestoneTasks(spec, m.id);
  const dependents = spec.roadmap.milestones.filter((x) => x.depends_on?.includes(m.id));
  const risks = spec.risks.risks.filter((r) => live(r) && r.milestones?.includes(m.id));
  const title = (id: string) => spec.roadmap.milestones.find((x) => x.id === id)?.title ?? id;
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{m.title}</h3>
        <span className="aside-row">
          <StatusPill status={m.status} />
          <EditButton kind="milestone" id={m.id} />
        </span>
      </div>
      <p className="muted">
        <Code>{m.id}</Code>
        {m.target && ` · target ${m.target}`}
      </p>
      <p>{m.goal}</p>
      {blocked.length > 0 && (
        <div className="banner bad">
          Blocked by{" "}
          {blocked.map((id, i) => (
            <span key={id}>
              {i > 0 && ", "}
              <a href={`#/roadmap/${id}`}>{title(id)}</a>
            </span>
          ))}
        </div>
      )}

      <h4 className="sub">
        Exit criteria {total > 0 && `(${done}/${total})`}
      </h4>
      {total === 0 ? (
        <Empty>None defined. Add criteria so “done” is unambiguous.</Empty>
      ) : (
        <ul className="criteria">
          {m.exit_criteria!.map((c, i) => (
            <li key={i} className={c.done ? "met" : ""}>
              <span aria-hidden>{c.done ? "☑" : "☐"}</span> {c.text}
            </li>
          ))}
        </ul>
      )}

      <dl className="facts">
        {!!m.depends_on?.length && (
          <>
            <dt>Depends on</dt>
            <dd>
              {m.depends_on.map((id) => (
                <div key={id}>
                  <a href={`#/roadmap/${id}`}>{title(id)}</a>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!dependents.length && (
          <>
            <dt>Unblocks</dt>
            <dd>
              {dependents.map((x) => (
                <div key={x.id}>
                  <a href={`#/roadmap/${x.id}`}>{x.title}</a>
                </div>
              ))}
            </dd>
          </>
        )}
        {!!m.components?.length && (
          <>
            <dt>Components</dt>
            <dd>
              {m.components.map((id) => {
                const n = componentFindings(report, id).length;
                return (
                  <div key={id}>
                    <a href={`#/architecture/${id}`}>{id}</a>{" "}
                    {report && <span className={n ? "pill pill-bad" : "pill pill-good"}>{n ? `${n} findings` : "healthy"}</span>}
                  </div>
                );
              })}
            </dd>
          </>
        )}
        {!!m.decisions?.length && (
          <>
            <dt>Decisions</dt>
            <dd>
              {m.decisions.map((id) => (
                <div key={id}>
                  <a href={`#/decisions/${id}`}>
                    {id} {spec.decisions.find((d) => d.id === id)?.title}
                  </a>
                </div>
              ))}
            </dd>
          </>
        )}
      </dl>

      {risks.length > 0 && (
        <>
          <h4 className="sub">Risks</h4>
          <ul className="plain">
            {risks.map((r) => (
              <li key={r.id}>
                <span className={`sev-badge sev-${severityLabel(severity(r))}`}>{severity(r)}</span> <a href={`#/risks/${r.id}`}>{r.title}</a>
              </li>
            ))}
          </ul>
        </>
      )}

      <h4 className="sub">Tasks</h4>
      {tasks.length === 0 ? (
        <Empty>No tasks yet. Target this milestone when you create a proposal.</Empty>
      ) : (
        <ul className="plain">
          {tasks.map((t) => (
            <li key={t.id}>
              <StatusPill status={t.status} /> <a href={`#/tasks/${t.id}`}>{t.agent}: {t.title}</a>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
