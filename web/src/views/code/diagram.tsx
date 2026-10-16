import dagre from "@dagrejs/dagre";
import { useEffect, useRef, useState, type ReactNode, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from "react";

export interface GNode {
  id: string;
  width: number;
  height: number;
  /** A cluster to draw the node inside. */
  parent?: string;
}

export interface GEdge {
  id: string;
  from: string;
  to: string;
  /** Space to leave for a label at the edge's middle. */
  label?: string;
}

export interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface Laid {
  /** Top-left corners, by node and cluster id. */
  boxes: Map<string, Box>;
  edges: Map<string, { d: string; mid: { x: number; y: number } }>;
  width: number;
  height: number;
}

const MARGIN = 24;
const MIN_SCALE = 0.8;
const MIN_H = 260;
const MAX_H = 720;

/** Lays out a directed graph with dagre: nodes in ranks along rankdir. */
export function layoutGraph(
  nodes: GNode[],
  edges: GEdge[],
  { rankdir = "TB", clusters = [], nodesep = 36, ranksep = 64 }: { rankdir?: "TB" | "LR"; clusters?: string[]; nodesep?: number; ranksep?: number } = {},
): Laid {
  const g = new dagre.graphlib.Graph({ multigraph: true, compound: clusters.length > 0 });
  g.setGraph({ rankdir, nodesep, ranksep, marginx: MARGIN, marginy: MARGIN });
  g.setDefaultEdgeLabel(() => ({}));
  for (const c of clusters) g.setNode(c, { paddingTop: 30, paddingBottom: 14, paddingLeft: 14, paddingRight: 14 });
  for (const n of nodes) {
    g.setNode(n.id, { width: n.width, height: n.height });
    if (n.parent) g.setParent(n.id, n.parent);
  }
  const known = new Set(nodes.map((n) => n.id));
  for (const e of edges) {
    if (!known.has(e.from) || !known.has(e.to) || e.from === e.to) continue;
    g.setEdge(e.from, e.to, e.label ? { width: e.label.length * 6.4, height: 14, labelpos: "c" } : {}, e.id);
  }
  dagre.layout(g);
  const boxes = new Map<string, Box>();
  for (const id of [...clusters, ...nodes.map((n) => n.id)]) {
    const n = g.node(id);
    if (!n || n.x === undefined) continue;
    boxes.set(id, { x: n.x - n.width / 2, y: n.y - n.height / 2, width: n.width, height: n.height });
  }
  const out = new Map<string, { d: string; mid: { x: number; y: number } }>();
  for (const e of g.edges()) {
    const pts = g.edge(e).points ?? [];
    if (!pts.length || !e.name) continue;
    out.set(e.name, { d: smooth(pts), mid: pts[Math.floor(pts.length / 2)] });
  }
  const graph = g.graph();
  return { boxes, edges: out, width: Math.max(graph.width ?? 0, 200), height: Math.max(graph.height ?? 0, 120) };
}

/** A path through points, rounded at the bends. */
function smooth(pts: { x: number; y: number }[]) {
  let d = `M ${pts[0].x} ${pts[0].y}`;
  for (let i = 1; i < pts.length - 1; i++) {
    const p = pts[i];
    const n = pts[i + 1];
    d += ` Q ${p.x} ${p.y} ${(p.x + n.x) / 2} ${(p.y + n.y) / 2}`;
  }
  const last = pts[pts.length - 1];
  return d + ` L ${last.x} ${last.y}`;
}

/** Approximate width of monospace text at the diagrams' code font size. */
export const textWidth = (s: string) => s.length * 6.7;

/** Approximate width of a bold title at the diagrams' title size. */
export const titleWidth = (s: string) => s.length * 8;

export const clip = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + "…" : s);

/**
 * An svg canvas that fits its content, pans by dragging the background,
 * and zooms with ⌘/Ctrl + wheel (or a pinch) and its buttons.
 */
export function Canvas({ width, height, label, children, defs }: { width: number; height: number; label: string; children: ReactNode; defs?: ReactNode }) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const drag = useRef<{ x: number; y: number; vx: number; vy: number; id: number } | null>(null);
  const [frame, setFrame] = useState(900);
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setFrame(el.clientWidth || 900));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  // Fit the diagram, but no smaller than a readable scale: past that, it
  // starts at the top left and pans.
  const fitScale = Math.min(1, frame / width, MAX_H / height);
  const scale0 = Math.max(fitScale, MIN_SCALE);
  const pxHeight = Math.max(MIN_H, Math.min(MAX_H, height * scale0));
  const initial = () => {
    const w = frame / scale0;
    const h = pxHeight / scale0;
    // Center what fits; start at the top left otherwise.
    return { x: width < w ? (width - w) / 2 : 0, y: height < h ? (height - h) / 2 : 0, w, h };
  };
  const [view, setView] = useState(initial);
  // Refit when the content or the frame changes.
  useEffect(() => setView(initial()), [width, height, frame]); // eslint-disable-line react-hooks/exhaustive-deps
  const fit = () => {
    const s = Math.min(frame / width, pxHeight / height);
    const w = frame / s;
    const h = pxHeight / s;
    setView({ x: (width - w) / 2, y: (height - h) / 2, w, h });
  };

  const scale = () => {
    const r = svgRef.current?.getBoundingClientRect();
    return r && r.width ? view.w / r.width : 1;
  };
  const zoom = (factor: number, cx = view.x + view.w / 2, cy = view.y + view.h / 2) => {
    const w = Math.min(Math.max(view.w * factor, frame / 4), Math.max(width, frame) * 4);
    const h = (w / view.w) * view.h;
    setView({ x: cx - ((cx - view.x) * w) / view.w, y: cy - ((cy - view.y) * h) / view.h, w, h });
  };
  const onWheel = (e: ReactWheelEvent) => {
    if (!e.ctrlKey && !e.metaKey) return;
    const r = svgRef.current!.getBoundingClientRect();
    const s = view.w / r.width;
    zoom(Math.exp(e.deltaY * 0.01), view.x + (e.clientX - r.left) * s, view.y + (e.clientY - r.top) * s);
  };
  // Only the background pans, so nodes stay clickable.
  const onPointerDown = (e: ReactPointerEvent) => {
    if (e.button !== 0 || e.target !== e.currentTarget) return;
    drag.current = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y, id: e.pointerId };
    svgRef.current!.setPointerCapture(e.pointerId);
  };
  const onPointerMove = (e: ReactPointerEvent) => {
    const d = drag.current;
    if (!d) return;
    const s = scale();
    setView((v) => ({ ...v, x: d.vx - (e.clientX - d.x) * s, y: d.vy - (e.clientY - d.y) * s }));
  };
  const onPointerUp = () => {
    drag.current = null;
  };
  // React's wheel listener is passive, so a native one stops the page from
  // zooming along with the diagram.
  useEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const block = (e: WheelEvent) => (e.ctrlKey || e.metaKey) && e.preventDefault();
    svg.addEventListener("wheel", block, { passive: false });
    return () => svg.removeEventListener("wheel", block);
  }, []);

  return (
    <div className="canvas" ref={wrapRef}>
      <div className="canvas-tools">
        <button className="btn btn-ghost btn-small" onClick={() => zoom(0.8)} aria-label="Zoom in" title="Zoom in (⌘/Ctrl + wheel)">
          +
        </button>
        <button className="btn btn-ghost btn-small" onClick={() => zoom(1.25)} aria-label="Zoom out" title="Zoom out">
          −
        </button>
        <button className="btn btn-ghost btn-small" onClick={fit} title="Show the whole diagram">
          Fit
        </button>
      </div>
      <svg
        ref={svgRef}
        className="diagram code-diagram"
        viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`}
        width="100%"
        style={{ height: pxHeight }}
        role="img"
        aria-label={label}
        onWheel={onWheel}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
      >
        <defs>
          {/* UML relation ends. */}
          <marker id="uml-open" viewBox="0 0 12 12" refX="11" refY="6" markerWidth="10" markerHeight="10" orient="auto-start-reverse">
            <path d="M 1 1 L 11 6 L 1 11" className="uml-head open" />
          </marker>
          <marker id="uml-triangle" viewBox="0 0 14 14" refX="13" refY="7" markerWidth="12" markerHeight="12" orient="auto-start-reverse">
            <path d="M 1 1 L 13 7 L 1 13 z" className="uml-head hollow" />
          </marker>
          <marker id="uml-diamond" viewBox="0 0 16 10" refX="15" refY="5" markerWidth="13" markerHeight="9" orient="auto-start-reverse">
            <path d="M 1 5 L 8 1 L 15 5 L 8 9 z" className="uml-head filled" />
          </marker>
          {defs}
        </defs>
        {/* The background catches drags for panning. */}
        <rect x={view.x - width * 4} y={view.y - height * 4} width={width * 9} height={height * 9} className="canvas-bg" onPointerDown={onPointerDown} />
        {children}
      </svg>
    </div>
  );
}
