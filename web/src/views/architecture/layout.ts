import type { Component, Dependency, Spec } from "../../types";

/** Whether a component has code to drill into. */
export const hasCode = (c: Component) => !!(c.paths?.length || c.contracts?.length);

export const NODE_W = 196;
export const NODE_H = 78;
export const GAP_X = 84;
export const ROW_H = 150;
export const LABEL_W = 120;
export const PAD = 24;
export const MIN_W = 900;

export interface Placed {
  c: Component;
  x: number;
  y: number;
  row: number;
}

export interface Edge {
  from: Placed;
  to: Placed;
  /** For event flows, protocol is "event" and the label names the event. */
  dep: Pick<Dependency, "target"> & { protocol: Dependency["protocol"] | "event"; label?: string };
}

/** Space around a context's components inside its outline. The name sits on
 * the top border, so the top needs only a little more. */
export const CTX_PAD = 12;
export const CTX_TOP = 18;
/** Contexts with no components yet are small tags, stacked in one column. */
export const GHOST_W = 150;
export const GHOST_H = 28;
const GHOST_GAP = 8;

/** One bounded context's outline in one row. A context with no components
 * yet is a ghost: a small dashed tag beside the row's components. */
export interface ContextBox {
  id: string;
  name: string;
  status: string;
  /** The context's index in domains.yaml, which picks its colour. */
  index: number;
  x: number;
  y: number;
  w: number;
  h: number;
  ghost: boolean;
}

/** Places components in rows, one per layer in spec order. Within a row,
 * the components of each bounded context sit together, in the order of
 * domains.yaml, so one outline can enclose them; then those without one. */
export function layout(spec: Spec) {
  const domains = spec.domains.domains;
  // A component in several contexts sits with the first one.
  const contextOf = new Map<string, number>();
  domains.forEach((d, i) => d.components?.forEach((c) => contextOf.has(c) || contextOf.set(c, i)));
  const rank = (c: Component) => contextOf.get(c.id) ?? domains.length;
  const rows = spec.system.layers.map((l) =>
    spec.system.components
      .map((c, i) => ({ c, i }))
      .filter(({ c }) => c.layer === l.id)
      .sort((a, b) => rank(a.c) - rank(b.c) || a.i - b.i)
      .map(({ c }) => c),
  );
  // Contexts without placed components show as ghosts at the end of the row
  // holding the most context members.
  const ghosts = domains.map((_, i) => i).filter((i) => !rows.some((row) => row.some((c) => contextOf.get(c.id) === i)));
  const members = rows.map((row) => row.filter((c) => contextOf.has(c.id)).length);
  // Without any members yet, the row with the most services.
  const weight = Math.max(...members) ? members : rows.map((row) => row.filter((c) => c.kind === "service").length);
  const ghostRow = ghosts.length ? weight.indexOf(Math.max(...weight)) : -1;
  const rowWidthOf = (r: number) => {
    const n = rows[r].length;
    const nodes = n * NODE_W + Math.max(0, n - 1) * GAP_X;
    return r === ghostRow ? nodes + (n ? GAP_X / 2 : 0) + GHOST_W : nodes;
  };
  const widest = Math.max(NODE_W, ...rows.map((_, r) => rowWidthOf(r)));
  const width = Math.max(MIN_W, LABEL_W + PAD * 2 + widest);
  const placed = new Map<string, Placed>();
  const contexts: ContextBox[] = [];
  const box = (i: number, x0: number, x1: number, y: number, ghost: boolean) =>
    contexts.push({
      id: domains[i].id,
      name: domains[i].name,
      status: domains[i].status,
      index: i,
      x: x0 - CTX_PAD,
      y: y - CTX_TOP,
      w: x1 - x0 + CTX_PAD * 2,
      h: NODE_H + CTX_TOP + CTX_PAD,
      ghost,
    });
  rows.forEach((row, r) => {
    const start = LABEL_W + PAD + (width - LABEL_W - PAD * 2 - rowWidthOf(r)) / 2;
    const y = PAD + r * ROW_H + (ROW_H - NODE_H) / 2;
    row.forEach((c, i) => placed.set(c.id, { c, x: start + i * (NODE_W + GAP_X), y, row: r }));
    // The row's components of a context are adjacent, so their span is one box.
    const seen = new Set<number>();
    for (const c of row) {
      const i = contextOf.get(c.id);
      if (i === undefined || seen.has(i)) continue;
      seen.add(i);
      const xs = row.filter((m) => contextOf.get(m.id) === i).map((m) => placed.get(m.id)!.x);
      box(i, Math.min(...xs), Math.max(...xs) + NODE_W, y, false);
    }
    if (r === ghostRow) {
      const x = start + (row.length ? row.length * (NODE_W + GAP_X) - GAP_X / 2 : 0);
      const stack = ghosts.length * GHOST_H + (ghosts.length - 1) * GHOST_GAP;
      const top = PAD + r * ROW_H + (ROW_H - stack) / 2;
      ghosts.forEach((i, k) => {
        const d = domains[i];
        contexts.push({ id: d.id, name: d.name, status: d.status, index: i, x, y: top + k * (GHOST_H + GHOST_GAP), w: GHOST_W, h: GHOST_H, ghost: true });
      });
    }
  });
  const edges: Edge[] = [];
  for (const p of placed.values()) {
    for (const dep of p.c.depends_on ?? []) {
      const to = placed.get(dep.target);
      if (to) edges.push({ from: p, to, dep });
    }
  }
  // Event flows run from producer to each consumer.
  for (const e of spec.events.events) {
    const from = placed.get(e.producer);
    if (!from || e.status === "deprecated") continue;
    for (const c of e.consumers ?? []) {
      const to = placed.get(c);
      if (to) edges.push({ from, to, dep: { target: c, protocol: "event", label: "event" } });
    }
  }
  return { placed, edges, contexts, contextOf, width, height: PAD * 2 + rows.length * ROW_H };
}

export function edgePath(e: Edge, fromSlot: number, toSlot: number) {
  const { from, to } = e;
  if (from.row === to.row) {
    const leftToRight = from.x < to.x;
    const sx = leftToRight ? from.x + NODE_W : from.x;
    const tx = leftToRight ? to.x : to.x + NODE_W;
    const y = from.y + NODE_H / 2;
    return { d: `M ${sx} ${y} L ${tx} ${y}`, mx: (sx + tx) / 2, my: y };
  }
  const down = to.row > from.row;
  const sx = from.x + NODE_W / 2 + fromSlot;
  const tx = to.x + NODE_W / 2 + toSlot;
  const sy = down ? from.y + NODE_H : from.y;
  const ty = down ? to.y : to.y + NODE_H;
  const bend = (ty - sy) / 2;
  return {
    d: `M ${sx} ${sy} C ${sx} ${sy + bend}, ${tx} ${ty - bend}, ${tx} ${ty}`,
    mx: (sx + tx) / 2,
    my: (sy + ty) / 2,
  };
}

/** Spreads edges that share an endpoint so their arrows don't overlap. */
export function slots(edges: Edge[], key: (e: Edge) => string) {
  const groups = new Map<string, Edge[]>();
  for (const e of edges) groups.set(key(e), [...(groups.get(key(e)) ?? []), e]);
  const out = new Map<Edge, number>();
  for (const g of groups.values()) {
    g.sort((a, b) => a.to.x - b.to.x || a.from.x - b.from.x);
    g.forEach((e, i) => out.set(e, (i - (g.length - 1) / 2) * 22));
  }
  return out;
}
