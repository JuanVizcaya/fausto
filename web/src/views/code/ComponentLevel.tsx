import type { Component, Spec } from "../../types";
import { Canvas, clip, layoutGraph, textWidth, titleWidth, type GEdge, type GNode } from "./diagram";
import { CodeIndex, codeItem, shortDir } from "./model";

const LINE = 16;

interface Node extends GNode {
  kind: "package" | "neighbor" | "dependent" | "contract" | "database";
  title: string;
  stereotype: string;
  lines: string[];
  target?: string;
  role?: string;
}

/**
 * Level 2: the component's packages and the imports between them, with its
 * contracts and database, and the packages of other components it imports
 * or that import it.
 */
export function ComponentLevel({ c, code, spec, nav }: { c: Component; code: CodeIndex; spec: Spec; nav: (item: string) => void }) {
  const m = code.model;
  const own = `c:${c.id}`;
  const name = (id?: string) => spec.system.components.find((x) => x.id === id)?.name ?? id ?? "other";
  const nodes: Node[] = [];
  const edges: (GEdge & { kind: string })[] = [];
  const clusters = new Set<string>([own]);
  const box = (n: Omit<Node, "width" | "height">) => {
    const width = Math.max(190, titleWidth(n.title) + 28, ...n.lines.map((l) => textWidth(l) + 28));
    nodes.push({ ...n, width: Math.min(width, 380), height: 44 + n.lines.length * LINE });
  };

  for (const p of m.packages) {
    box({
      id: `pkg:${p.dir}`,
      parent: own,
      kind: "package",
      role: p.role,
      stereotype: p.role === "cmd" ? "«main»" : p.role === "generated" ? "«generated»" : "«package»",
      title: shortDir(p.dir),
      lines: [`${p.types.length} types · ${p.funcs.length} funcs · ${p.files.length} files`],
      target: codeItem(c.id, p.dir),
    });
    for (const imp of p.imports) {
      const to = imp.kind === "same" ? `pkg:${imp.dir}` : `nb:${imp.dir}`;
      edges.push({ id: `${p.dir}>${imp.dir}`, from: `pkg:${p.dir}`, to, kind: imp.kind });
    }
  }
  for (const n of m.neighbors) {
    const cl = `c:${n.component ?? "other"}`;
    clusters.add(cl);
    box({
      id: `nb:${n.dir}`,
      parent: cl,
      kind: "neighbor",
      stereotype: n.kind === "contract" ? "«contract»" : n.kind === "library" ? "«library»" : "«package»",
      title: shortDir(n.dir),
      lines: [],
      target: n.component ? codeItem(n.component, n.dir) : undefined,
    });
  }
  const dependents = new Map<string, string | undefined>();
  for (const d of m.dependents) {
    dependents.set(d.from, d.component);
    edges.push({ id: `${d.from}>${d.to}`, from: `dep:${d.from}`, to: `pkg:${d.to}`, kind: "dependent" });
  }
  for (const [dir, comp] of dependents) {
    const cl = `c:${comp ?? "other"}`;
    clusters.add(cl);
    box({
      id: `dep:${dir}`,
      parent: cl,
      kind: "dependent",
      stereotype: "«package»",
      title: shortDir(dir),
      lines: [],
      target: comp ? codeItem(comp, dir) : undefined,
    });
  }
  for (const ct of m.contracts) {
    const rpcs = ct.services.flatMap((s) => s.rpcs.map((r) => clip(`${r.name}(${r.clientStreams ? "stream " : ""}${r.request}) → ${r.serverStreams ? "stream " : ""}${r.response}`, 54)));
    box({
      id: `ct:${ct.proto}`,
      parent: own,
      kind: "contract",
      stereotype: "«proto»",
      title: ct.services.map((s) => s.name).join(", ") || ct.proto.split("/").slice(-2).join("/"),
      lines: rpcs.length ? rpcs : [`${ct.messages.length} messages`],
    });
    if (ct.generated && m.packages.some((p) => p.dir === ct.generated)) {
      edges.push({ id: `${ct.proto}>gen`, from: `ct:${ct.proto}`, to: `pkg:${ct.generated}`, kind: "generates", label: "generates" });
    }
  }
  const dbs = (c.depends_on ?? []).filter((d) => d.protocol === "sql").map((d) => spec.system.components.find((x) => x.id === d.target));
  for (const db of dbs) {
    if (!db) continue;
    box({
      id: `db:${db.id}`,
      kind: "database",
      stereotype: "«database»",
      title: db.name,
      lines: (c.data ?? []).map((t) => `table ${t.table}`),
      target: db.id,
    });
    for (const p of m.packages.filter((p) => p.database)) {
      edges.push({ id: `${p.dir}>${db.id}`, from: `pkg:${p.dir}`, to: `db:${db.id}`, kind: "sql", label: "sql" });
    }
  }

  const laid = layoutGraph(nodes, edges, { rankdir: "LR", clusters: [...clusters], nodesep: 22, ranksep: 64 });
  return (
    <>
      <Canvas width={laid.width} height={laid.height} label={`Packages of ${c.name}`}>
        {[...clusters].map((cl) => {
          const b = laid.boxes.get(cl);
          if (!b) return null;
          const id = cl.slice(2);
          return (
            <g key={cl} className={`cluster${cl === own ? " own" : ""}`}>
              <rect x={b.x} y={b.y} width={b.width} height={b.height} rx={12} />
              <text x={b.x + 12} y={b.y + 19} className="cluster-label">
                {cl === own ? c.name : name(id)}
              </text>
            </g>
          );
        })}
        {edges.map((e) => {
          const p = laid.edges.get(e.id);
          if (!p) return null;
          const dashed = e.kind === "generates" || e.kind === "dependent";
          return (
            <g key={e.id} className={`cedge cedge-${e.kind}`}>
              <path d={p.d} markerEnd="url(#uml-open)" strokeDasharray={dashed ? "5 4" : undefined} />
              {e.label && (
                <text x={p.mid.x} y={p.mid.y - 4} textAnchor="middle" className="edge-label">
                  {e.label}
                </text>
              )}
            </g>
          );
        })}
        {nodes.map((n) => {
          const b = laid.boxes.get(n.id);
          if (!b) return null;
          const go = n.target ? () => nav(n.target!) : undefined;
          return (
            <g
              key={n.id}
              className={`cnode cnode-${n.kind}${n.role ? ` role-${n.role}` : ""}${go ? " clickable" : ""}`}
              transform={`translate(${b.x} ${b.y})`}
              onClick={go}
              onKeyDown={(e) => go && (e.key === "Enter" || e.key === " ") && go()}
              tabIndex={go ? 0 : undefined}
              role={go ? "button" : undefined}
              aria-label={`${n.stereotype} ${n.title}`}
            >
              <rect width={b.width} height={b.height} rx={n.kind === "database" ? 14 : 8} />
              <text x={b.width / 2} y={16} textAnchor="middle" className="stereotype">
                {n.stereotype}
              </text>
              <text x={b.width / 2} y={33} textAnchor="middle" className="cnode-title">
                <title>{n.title}</title>
                {clip(n.title, 50)}
              </text>
              {n.lines.map((l, i) => (
                <text key={i} x={12} y={54 + i * LINE} className="cnode-line">
                  {l}
                </text>
              ))}
            </g>
          );
        })}
      </Canvas>
      <p className="hint">
        Click a package to open its class diagram. Arrows are Go imports; dimmed boxes belong to other components, which import this one
        (dashed) or are imported by it.
      </p>
    </>
  );
}
