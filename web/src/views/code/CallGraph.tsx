import { Canvas, clip, layoutGraph, textWidth, titleWidth, type GEdge, type GNode } from "./diagram";
import { CodeIndex, shortDir, splitId } from "./model";

const MAX_NODES = 48;

type Kind = "call" | "impl" | "rpc";

interface Node extends GNode {
  title: string;
  sub: string;
  role: "center" | "caller" | "callee" | "interface";
  foreign: boolean;
}

/**
 * Level 4 for a function or method: who calls it (left) and what it calls,
 * two calls deep (right). A call to an interface method continues to the
 * methods that implement it.
 */
export function CallGraph({ id, code, nav }: { id: string; code: CodeIndex; nav: (item: string) => void }) {
  const nodes = new Map<string, Node>();
  const edges: (GEdge & { kind: Kind })[] = [];
  const own = new Set(code.model.packages.map((p) => p.dir));
  const add = (sym: string, role: Node["role"]) => {
    if (nodes.has(sym)) return true;
    if (nodes.size >= MAX_NODES) return false;
    const { dir } = splitId(sym);
    const title = code.label(sym);
    const isIface = code.implementations(sym).length > 0 || code.symbols.get(sym)?.type?.kind === "interface";
    nodes.set(sym, {
      id: sym,
      title,
      sub: shortDir(dir),
      role: isIface && role !== "center" ? "interface" : role,
      foreign: !own.has(dir),
      width: Math.min(340, Math.max(150, titleWidth(title) + 26, textWidth(shortDir(dir)) + 26)),
      height: 46,
    });
    return true;
  };
  const edge = (from: string, to: string, kind: Kind) => {
    if (!edges.some((e) => e.from === from && e.to === to)) edges.push({ id: `${from}>${to}`, from, to, kind, label: kind === "rpc" ? "gRPC" : undefined });
  };
  const call = (from: string, to: string) => edge(from, to, code.isRPC(from, to) ? "rpc" : "call");

  add(id, "center");
  // Callers two levels back, so a gRPC call shows the handler that made it.
  let callers = [id];
  for (let depth = 0; depth < 2; depth++) {
    const next: string[] = [];
    for (const target of callers) {
      for (const c of code.callers(target)) {
        if (!add(c.id, "caller")) break;
        if (c.via && add(c.via, "interface")) {
          call(c.id, c.via);
          edge(c.via, target, "impl");
        } else {
          call(c.id, target);
        }
        next.push(c.id);
      }
    }
    callers = next;
  }
  // An interface method's callers reach its implementations.
  for (const impl of code.implementations(id)) {
    if (add(impl, "callee")) edge(id, impl, "impl");
  }
  let frontier = [id, ...code.implementations(id)];
  for (let depth = 0; depth < 2; depth++) {
    const next: string[] = [];
    for (const from of frontier) {
      for (const to of code.callees(from)) {
        if (!add(to, "callee")) break;
        call(from, to);
        next.push(to);
        for (const impl of code.implementations(to)) {
          if (add(impl, "callee")) {
            edge(to, impl, "impl");
            next.push(impl);
          }
        }
      }
    }
    frontier = next;
  }

  const list = [...nodes.values()];
  const laid = layoutGraph(list, edges, { rankdir: "LR", nodesep: 18, ranksep: 70 });
  return (
    <>
      <Canvas width={laid.width} height={laid.height} label={`Calls to and from ${code.label(id)}`}>
        {edges.map((e) => {
          const p = laid.edges.get(e.id);
          if (!p) return null;
          return (
            <g key={e.id} className={`call call-${e.kind}`}>
              <path
                d={p.d}
                markerStart={e.kind === "impl" ? "url(#uml-triangle)" : undefined}
                markerEnd={e.kind !== "impl" ? "url(#uml-open)" : undefined}
                strokeDasharray={e.kind === "impl" ? "6 4" : e.kind === "rpc" ? "2 3" : undefined}
              />
              {e.label && (
                <text x={p.mid.x} y={p.mid.y - 5} textAnchor="middle" className="edge-label">
                  {e.label}
                </text>
              )}
            </g>
          );
        })}
        {list.map((n) => {
          const b = laid.boxes.get(n.id);
          if (!b) return null;
          const target = n.role === "center" ? undefined : code.itemFor(n.id);
          return (
            <g
              key={n.id}
              className={`fnode fnode-${n.role}${n.foreign ? " foreign" : ""}${target ? " clickable" : ""}`}
              transform={`translate(${b.x} ${b.y})`}
              onClick={() => target && nav(target)}
              onKeyDown={(e) => target && e.key === "Enter" && nav(target)}
              tabIndex={target ? 0 : undefined}
              role={target ? "button" : undefined}
              aria-label={n.title}
            >
              <rect width={b.width} height={b.height} rx={n.role === "interface" ? 4 : 10} />
              <text x={12} y={20} className="fnode-title">
                <title>{n.title}</title>
                {clip(n.title, 46)}
              </text>
              <text x={12} y={36} className="fnode-sub">
                {n.role === "interface" ? "«interface» " : ""}
                {clip(n.sub, 44)}
              </text>
            </g>
          );
        })}
      </Canvas>
      <p className="hint">
        Callers on the left and calls on the right, two levels each way. A dashed line with a hollow triangle goes from an interface method to the
        method that implements it; a dotted gRPC line goes from a client method to the server method it reaches over the network. Faded boxes
        are in other components.
        {nodes.size >= MAX_NODES && ` Showing the first ${MAX_NODES} functions.`}
      </p>
    </>
  );
}
