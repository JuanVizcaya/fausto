import { useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from "react";
import type { ViewProps } from "../../App";
import { EditButton, putUpdate, targetFor, useEditor } from "../../editor";
import { applyDraft, orderKey, useDraft, type DraftMark } from "../../draft";
import { componentFindings } from "../../api";
import type { Dependency, Layer } from "../../types";
import { Section } from "../../ui";
import { codeItem } from "../code/model";
import { NODE_H, NODE_W, PAD, ROW_H, edgePath, hasCode, layout, slots, type Edge } from "./layout";
import { DependencyPopover, type DepPopover, type Protocol } from "./DependencyPopover";
import { Inspector, parseSelection, selectedComponents } from "./Inspector";

/** A pointer gesture in progress on the diagram. */
type Gesture =
  | { type: "move"; id: string; x0: number; y0: number; x: number; y: number; moved: boolean }
  | { type: "link"; id: string; x: number; y: number };

const kindIcon: Record<string, string> = {
  gateway: "⇄",
  service: "◼",
  database: "⛁",
  frontend: "▣",
  library: "❖",
};

/** A string setting kept in localStorage; storage can be unavailable, so
 * failures fall back to the in-memory value. */
function useStored(key: string, initial: string): [string, (v: string) => void] {
  const [value, setValue] = useState(() => {
    try {
      return localStorage.getItem(key) ?? initial;
    } catch {
      return initial;
    }
  });
  const set = (v: string) => {
    setValue(v);
    try {
      localStorage.setItem(key, v);
    } catch {
      // Not remembered; the setting still applies for this visit.
    }
  };
  return [value, set];
}

/** How many context colours styles.css defines (.ctx-0 …). */
const CTX_COLORS = 6;

/** A layer's dependency rule as "→ a, b", in lines short enough for the
 * label column. */
function ruleLines(l: Layer, max = 18) {
  if (!l.may_depend_on.length) return ["→ none"];
  const lines: string[] = [];
  let line = "→";
  l.may_depend_on.forEach((id, i) => {
    const word = id + (i < l.may_depend_on.length - 1 ? "," : "");
    if (line.length + 1 + word.length > max && line !== "→") {
      lines.push(line);
      line = "  " + word;
    } else line += " " + word;
  });
  return [...lines, line];
}

/** Level 1: components in their layers, editable. */
export function SystemView({ spec: saved, report, go, item, repo, branch }: ViewProps & { repo?: string; branch: string }) {
  // A layer's description and dependency rule, shown when hovering its name.
  const layerHint = (l: Layer) =>
    [l.description, l.may_depend_on.length ? `May depend on: ${l.may_depend_on.join(", ")}` : "May not depend on other layers."]
      .filter(Boolean)
      .join("\n");
  const [showLibraries, setShowLibraries] = useState(false);
  // "", "contexts", or "tech:<id>"; remembered per browser.
  const [highlight, setHighlight] = useStored("fausto.arch.highlight", "");
  const [showContexts, setShowContexts] = useStored("fausto.arch.contexts", "on");
  const draft = useDraft();
  const { editable, edit } = useEditor();
  // The diagram previews the draft: added, edited, and deleted items are marked.
  const { spec, marks } = useMemo(() => applyDraft(saved, draft.changes, true), [saved, draft.changes]);
  const { placed, edges, contexts, contextOf, width, height } = useMemo(() => layout(spec), [spec]);
  // Hovering one segment of a context's outline highlights all of them.
  const [hoverContext, setHoverContext] = useState<string | null>(null);
  const sel = parseSelection(spec, item);
  const selected = sel?.kind === "component" ? sel.c : undefined;
  // A selected layer or context brings its components forward and fades the rest.
  const focus = sel && sel.kind !== "component" ? selectedComponents(spec, sel) : undefined;
  // Highlighting a technology fades the components that don't use it.
  const tech = highlight.startsWith("tech:") ? spec.tech.technologies.find((t) => t.id === highlight.slice(5)) : undefined;
  const techUsers = tech && new Set(spec.system.components.filter((c) => c.tech?.includes(tech.id)).map((c) => c.id));
  const tint = highlight === "contexts";
  const outside = (id: string) => (focus && !focus.has(id)) || (techUsers && !techUsers.has(id));
  const selectLayer = (id: string) => go("architecture", sel?.kind === "layer" && sel.layer.id === id ? undefined : `layer:${id}`);
  const mark = (id: string): DraftMark | undefined => marks.get(`component:${id}`);

  const visible = edges.filter((e) => showLibraries || e.dep.protocol !== "library");
  const outSlots = slots(visible, (e) => e.from.c.id);
  const inSlots = slots(visible, (e) => e.to.c.id);
  // Edges between the same two components (a gRPC call one way and an
  // event the other) are spread apart so they and their labels don't overlap.
  const pairOffset = new Map<Edge, number>();
  const pairs = new Map<string, Edge[]>();
  for (const e of visible) {
    const key = [e.from.c.id, e.to.c.id].sort().join("|");
    pairs.set(key, [...(pairs.get(key) ?? []), e]);
  }
  for (const group of pairs.values()) {
    group.forEach((e, i) => {
      const shift = (i - (group.length - 1) / 2) * 44;
      // The shift is in the same screen direction for both ends.
      pairOffset.set(e, shift);
    });
  }
  const highlighted = (e: Edge) => selected && (e.from.c.id === selected.id || e.to.c.id === selected.id);

  // Direct editing: drag a node to another layer, drag its handle onto
  // another node to add a dependency, click an arrow to change it.
  const svgRef = useRef<SVGSVGElement>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const [gesture, setGesture] = useState<Gesture | null>(null);
  const [popover, setPopover] = useState<DepPopover | null>(null);
  const canEdit = (id: string) => editable && mark(id) !== "deleted";

  /** Converts a pointer position to diagram coordinates. */
  const toSvg = (clientX: number, clientY: number) => {
    const svg = svgRef.current!;
    const pt = svg.createSVGPoint();
    pt.x = clientX;
    pt.y = clientY;
    return pt.matrixTransform(svg.getScreenCTM()!.inverse());
  };
  /** Converts a pointer position to coordinates inside the scrolling wrapper. */
  const toWrap = (clientX: number, clientY: number) => {
    const wrap = wrapRef.current!;
    const r = wrap.getBoundingClientRect();
    // Keep the popover inside the wrapper, which clips it: flip it above the
    // pointer near the bottom and pull it in from the sides.
    const left = Math.min(Math.max(clientX - r.left, 150), r.width - 150) + wrap.scrollLeft;
    const top = clientY - r.top + wrap.scrollTop;
    return { left, top, above: clientY - r.top > r.height - 200 };
  };
  const layerAt = (y: number) => spec.system.layers[Math.min(spec.system.layers.length - 1, Math.max(0, Math.floor((y - PAD) / ROW_H)))];
  const nodeAt = (x: number, y: number) =>
    [...placed.values()].find((p) => x >= p.x && x <= p.x + NODE_W && y >= p.y && y <= p.y + NODE_H);

  const setDependency = (from: string, to: string, protocol: Protocol | null) =>
    putUpdate(draft, spec, "component", from, (v) => {
      const deps = ((v.depends_on as Dependency[] | undefined) ?? []).filter((d) => d.target !== to);
      const next = protocol ? [...deps, { target: to, protocol }] : deps;
      const out: Record<string, unknown> = { ...v, depends_on: next };
      if (!next.length) delete out.depends_on;
      return out;
    }, saved);

  const startMove = (e: ReactPointerEvent, id: string) => {
    if (!canEdit(id) || e.button !== 0) return;
    const { x, y } = toSvg(e.clientX, e.clientY);
    svgRef.current!.setPointerCapture(e.pointerId);
    setPopover(null);
    setGesture({ type: "move", id, x0: x, y0: y, x, y, moved: false });
  };
  const startLink = (e: ReactPointerEvent, id: string) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    const { x, y } = toSvg(e.clientX, e.clientY);
    svgRef.current!.setPointerCapture(e.pointerId);
    setPopover(null);
    setGesture({ type: "link", id, x, y });
  };
  const onPointerMove = (e: ReactPointerEvent) => {
    if (!gesture) return;
    const { x, y } = toSvg(e.clientX, e.clientY);
    if (gesture.type === "move") {
      const moved = gesture.moved || Math.hypot(x - gesture.x0, y - gesture.y0) > 5;
      setGesture({ ...gesture, x, y, moved });
    } else {
      setGesture({ ...gesture, x, y });
    }
  };
  const onPointerUp = (e: ReactPointerEvent) => {
    if (!gesture) return;
    setGesture(null);
    const { x, y } = toSvg(e.clientX, e.clientY);
    if (gesture.type === "move") {
      if (!gesture.moved) {
        // The second click of a double-click keeps the selection.
        if (e.detail < 2) go("architecture", selected?.id === gesture.id ? undefined : gesture.id);
        return;
      }
      const layer = layerAt(y);
      const c = placed.get(gesture.id)?.c;
      if (c && layer && layer.id !== c.layer && marks.get(`layer:${layer.id}`) !== "deleted") {
        putUpdate(draft, spec, "component", c.id, (v) => ({ ...v, layer: layer.id }), saved);
      }
      return;
    }
    const target = nodeAt(x, y);
    if (!target || target.c.id === gesture.id || mark(target.c.id) === "deleted") return;
    const existing = placed.get(gesture.id)?.c.depends_on?.find((d) => d.target === target.c.id);
    setPopover({ from: gesture.id, to: target.c.id, protocol: existing?.protocol, ...toWrap(e.clientX, e.clientY) });
  };
  // Pointer capture sends clicks to the svg, so double-clicks are resolved here.
  const onDoubleClick = (e: ReactMouseEvent) => {
    if (!editable) return;
    const { x, y } = toSvg(e.clientX, e.clientY);
    const node = nodeAt(x, y);
    if (node) {
      if (mark(node.c.id) !== "deleted") edit(targetFor(draft, "component", node.c.id));
      return;
    }
    const layer = layerAt(y);
    if (layer && marks.get(`layer:${layer.id}`) !== "deleted") edit({ kind: "component", initial: { layer: layer.id, status: "planned" } });
  };
  /** Moves a layer up (by -1) or down (by 1) in the draft. */
  const moveLayer = (id: string, by: number) => {
    const ids = spec.system.layers.map((l) => l.id);
    const i = ids.indexOf(id);
    const j = i + by;
    if (i < 0 || j < 0 || j >= ids.length) return;
    [ids[i], ids[j]] = [ids[j], ids[i]];
    const key = orderKey("layer");
    const others = draft.changes.filter((c) => c.key !== key);
    const base = applyDraft(saved, others, true).spec.system.layers.map((l) => l.id);
    // The order goes last, so it also covers layers added earlier in the draft.
    draft.remove(key);
    if (base.join() !== ids.join()) draft.put({ key, kind: "layer", value: {}, order: ids, label: `Reorder layers: ${ids.join(", ")}` });
  };
  const moving = gesture?.type === "move" && gesture.moved ? gesture : null;
  const dropLayer = moving ? layerAt(moving.y) : undefined;

  return (
    <div className="split">
      <div className="split-main">
        <Section
          title="Containers"
          aside={
            <span className="aside-row">
              <select className="select" value={tech || tint ? highlight : ""} onChange={(e) => setHighlight(e.target.value)} aria-label="Highlight">
                <option value="">No highlight</option>
                <option value="contexts">Colour by context</option>
                <optgroup label="Components using">
                  {spec.tech.technologies.map((t) => (
                    <option key={t.id} value={`tech:${t.id}`}>
                      {t.name}
                    </option>
                  ))}
                </optgroup>
              </select>
              <label className="toggle">
                <input type="checkbox" checked={showContexts === "on"} onChange={(e) => setShowContexts(e.target.checked ? "on" : "off")} />
                Contexts
              </label>
              <label className="toggle">
                <input type="checkbox" checked={showLibraries} onChange={(e) => setShowLibraries(e.target.checked)} />
                Library dependencies
              </label>
              <EditButton kind="component" label="Add component" />
            </span>
          }
        >
          {spec.system.description && <p className="lede">{spec.system.description}</p>}
          <div className={`diagram-wrap${editable ? " editing" : ""}`} ref={wrapRef}>
            <svg
              ref={svgRef}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerCancel={() => setGesture(null)}
              onDoubleClick={onDoubleClick}
              className={`diagram${gesture ? ` gesture-${gesture.type}` : ""}`}
              viewBox={`0 0 ${width} ${height}`}
              width={width}
              role="img"
              aria-label="Component diagram by layer"
            >
              <defs>
                <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" className="arrowhead" />
                </marker>
                <marker id="arrow-hi" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" className="arrowhead hi" />
                </marker>
              </defs>
              {spec.system.layers.map((l, r) => {
                const lm = marks.get(`layer:${l.id}`);
                const top = PAD + r * ROW_H;
                return (
                  <g
                    key={l.id}
                    className={`band-group${lm ? ` draft-${lm}` : ""}${dropLayer?.id === l.id ? " drop" : ""}${sel?.kind === "layer" && sel.layer.id === l.id ? " selected" : ""}`}
                  >
                    <rect
                      x={PAD / 2}
                      y={top + 6}
                      width={width - PAD}
                      height={ROW_H - 12}
                      rx={12}
                      className="band"
                    />
                    <g
                      className="band-action"
                      role="button"
                      tabIndex={0}
                      aria-label={`Layer ${l.id}`}
                      onClick={() => selectLayer(l.id)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" || e.key === " ") selectLayer(l.id);
                        else if (editable && e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
                          e.preventDefault();
                          moveLayer(l.id, e.key === "ArrowUp" ? -1 : 1);
                        }
                      }}
                    >
                      <title>{editable ? `${layerHint(l)}\nAlt+↑/↓ moves it` : layerHint(l)}</title>
                      <text x={PAD} y={top + 30} className="band-label">
                        {l.id}
                      </text>
                    </g>
                    {/* The layers this one may depend on, wrapped to fit the label column. */}
                    <text y={top + 48} className="band-rule">
                      {ruleLines(l).map((line, i) => (
                        <tspan key={i} x={PAD} dy={i ? 14 : 0}>
                          {line}
                        </tspan>
                      ))}
                    </text>
                    {lm && (
                      <text x={PAD} y={top + ROW_H - 20} className="draft-tag">
                        {lm}
                      </text>
                    )}
                    {editable &&
                      (
                        [
                          [-1, "up", "▲", 70],
                          [1, "down", "▼", 42],
                        ] as const
                      ).map(([by, dir, glyph, offset]) => {
                        const off = r + by < 0 || r + by >= spec.system.layers.length;
                        return (
                          <g
                            key={dir}
                            className={`band-add band-move${off ? " off" : ""}`}
                            role="button"
                            tabIndex={off ? -1 : 0}
                            aria-disabled={off}
                            aria-label={`Move layer ${l.id} ${dir}`}
                            transform={`translate(${width - PAD - offset} ${top + 26})`}
                            onClick={() => !off && moveLayer(l.id, by)}
                            onKeyDown={(e) => !off && (e.key === "Enter" || e.key === " ") && moveLayer(l.id, by)}
                          >
                            <title>{`Move the layer ${dir}`}</title>
                            <circle r={11} />
                            <text textAnchor="middle" dy="3.5">
                              {glyph}
                            </text>
                          </g>
                        );
                      })}
                    {editable && lm !== "deleted" && (
                      <g
                        className="band-add"
                        role="button"
                        tabIndex={0}
                        aria-label={`Add component to layer ${l.id}`}
                        transform={`translate(${width - PAD - 14} ${top + 26})`}
                        onClick={() => edit({ kind: "component", initial: { layer: l.id, status: "planned" } })}
                        onKeyDown={(e) =>
                          (e.key === "Enter" || e.key === " ") && edit({ kind: "component", initial: { layer: l.id, status: "planned" } })
                        }
                      >
                        <title>Add a component to this layer</title>
                        <circle r={11} />
                        <text textAnchor="middle" dy="4.5">
                          +
                        </text>
                      </g>
                    )}
                  </g>
                );
              })}
              {showContexts === "on" && contexts.map((b) => {
                const isSel = sel?.kind === "context" && sel.domain.id === b.id;
                const select = () => go("architecture", isSel ? undefined : `context:${b.id}`);
                return (
                  <g
                    key={`${b.id}@${b.y}`}
                    className={`context ctx-${b.index % CTX_COLORS}${b.ghost ? " ghost" : ""}${isSel ? " selected" : ""}${hoverContext === b.id ? " hover" : ""}${focus && !isSel ? " faded" : ""}`}
                    role="button"
                    tabIndex={0}
                    aria-label={`Bounded context ${b.name}${b.ghost ? ", no components yet" : ""}`}
                    onClick={select}
                    onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && select()}
                    onPointerEnter={() => setHoverContext(b.id)}
                    onPointerLeave={() => setHoverContext(null)}
                  >
                    <title>{b.ghost ? `${b.name}: ${b.status}, no components yet` : b.name}</title>
                    <rect x={b.x} y={b.y} width={b.w} height={b.h} rx={b.ghost ? 8 : 14} className="context-box" />
                    {b.ghost && (
                      <text x={b.x + 10} y={b.y + b.h / 2 + 4} className="context-label">
                        {b.name}
                        <tspan className="context-empty"> · {b.status}</tspan>
                      </text>
                    )}
                  </g>
                );
              })}
              {visible.map((e, i) => {
                const shift = pairOffset.get(e) ?? 0;
                const { d, mx, my } = edgePath(e, (outSlots.get(e) ?? 0) + shift, (inSlots.get(e) ?? 0) + shift);
                const hi = highlighted(e);
                // Declared dependencies can be edited; event flows come from events.yaml.
                const clickable = e.dep.protocol !== "event" && canEdit(e.from.c.id);
                const open = (ev: { clientX: number; clientY: number }) =>
                  setPopover({ from: e.from.c.id, to: e.to.c.id, protocol: e.dep.protocol as Protocol, ...toWrap(ev.clientX, ev.clientY) });
                return (
                  <g
                    key={i}
                    className={`edge edge-${e.dep.protocol}${hi ? " hi" : ""}${(selected && !hi) || outside(e.from.c.id) || outside(e.to.c.id) ? " dim" : ""}${clickable ? " clickable" : ""}`}
                    onClick={clickable ? open : undefined}
                  >
                    {clickable && <path d={d} className="edge-hit" />}
                    <path d={d} markerEnd={hi ? "url(#arrow-hi)" : "url(#arrow)"} />
                    <g transform={`translate(${mx} ${my})`}>
                      <rect x={-20} y={-9} width={40} height={18} rx={9} className="edge-label-bg" />
                      <text textAnchor="middle" dy="4" className="edge-label">
                        {e.dep.protocol}
                      </text>
                    </g>
                  </g>
                );
              })}
              {/* Context names sit on their outline's top border, above the
                  arrows, so an arrow passing by never runs through one. */}
              {showContexts === "on" &&
                contexts
                  .filter((b) => !b.ghost)
                  .map((b) => {
                    const isSel = sel?.kind === "context" && sel.domain.id === b.id;
                    const w = b.name.length * 6.8 + 16;
                    return (
                      <g
                        key={`legend-${b.id}@${b.y}`}
                        className={`context context-legend ctx-${b.index % CTX_COLORS}${isSel ? " selected" : ""}${hoverContext === b.id ? " hover" : ""}${focus && !isSel ? " faded" : ""}`}
                        aria-hidden
                        onClick={() => go("architecture", isSel ? undefined : `context:${b.id}`)}
                        onPointerEnter={() => setHoverContext(b.id)}
                        onPointerLeave={() => setHoverContext(null)}
                      >
                        <rect x={b.x + 12} y={b.y - 9} width={w} height={18} rx={9} className="context-legend-bg" />
                        <text x={b.x + 20} y={b.y + 4} className="context-label">
                          {b.name}
                        </text>
                      </g>
                    );
                  })}
              {[...placed.values()].map((p) => {
                const findings = componentFindings(report, p.c.id).length;
                const isSel = selected?.id === p.c.id;
                const m = mark(p.c.id);
                const dx = moving?.id === p.c.id ? moving.x - moving.x0 : 0;
                const dy = moving?.id === p.c.id ? moving.y - moving.y0 : 0;
                const linkTarget = gesture?.type === "link" && gesture.id !== p.c.id && nodeAt(gesture.x, gesture.y) === p;
                return (
                  <g
                    key={p.c.id}
                    className={`node node-${p.c.kind} status-${p.c.status}${isSel ? " selected" : ""}${m ? ` draft-${m}` : ""}${dx || dy ? " dragging" : ""}${linkTarget ? " link-target" : ""}${outside(p.c.id) ? " faded" : ""}${tint && contextOf.has(p.c.id) ? ` tinted ctx-${contextOf.get(p.c.id)! % CTX_COLORS}` : ""}`}
                    transform={`translate(${p.x + dx} ${p.y + dy})`}
                    onPointerDown={(e) => startMove(e, p.c.id)}
                    onClick={() => !canEdit(p.c.id) && go("architecture", isSel ? undefined : p.c.id)}
                    tabIndex={0}
                    role="button"
                    aria-label={`${p.c.name}, ${p.c.kind}, ${p.c.status}`}
                    onKeyDown={(e) => {
                      // Enter on the selected component drills into its code.
                      if (e.key === "Enter" && isSel && hasCode(p.c)) go("architecture", codeItem(p.c.id));
                      else if (e.key === "Enter" || e.key === " ") go("architecture", p.c.id);
                    }}
                  >
                    <rect width={NODE_W} height={NODE_H} rx={10} className="node-box" />
                    <text x={14} y={26} className="node-icon">
                      {kindIcon[p.c.kind]}
                    </text>
                    <text x={34} y={26} className="node-title">
                      <title>{p.c.name}</title>
                      {p.c.name.length > 19 ? p.c.name.slice(0, 18) + "…" : p.c.name}
                    </text>
                    <text x={14} y={47} className="node-meta">
                      {p.c.kind} · {p.c.status}
                    </text>
                    <text x={14} y={65} className="node-meta">
                      {(p.c.tech ?? []).join(", ")}
                    </text>
                    {m && (
                      <text x={NODE_W - 12} y={NODE_H - 10} textAnchor="end" className="draft-tag">
                        {m}
                      </text>
                    )}
                    {report && p.c.status !== "planned" && !m && (
                      <g transform={`translate(${NODE_W - 16} 16)`}>
                        <circle r={8} className={findings ? "health bad" : "health good"} />
                        <text textAnchor="middle" dy="3.5" className="health-text">
                          {findings || "✓"}
                        </text>
                      </g>
                    )}
                    {hasCode(p.c) && !m && (
                      <g
                        className="zoom-handle"
                        transform={`translate(${NODE_W - 16} ${NODE_H - 16})`}
                        onPointerDown={(e) => e.stopPropagation()}
                        onClick={(e) => {
                          e.stopPropagation();
                          go("architecture", codeItem(p.c.id));
                        }}
                      >
                        <title>Open the code</title>
                        <circle r={9} />
                        <text textAnchor="middle" dy="4">
                          ⤵
                        </text>
                      </g>
                    )}
                    {canEdit(p.c.id) && (
                      <g className="link-handle" transform={`translate(${NODE_W / 2} ${NODE_H})`} onPointerDown={(e) => startLink(e, p.c.id)}>
                        <title>Drag onto another component to add a dependency</title>
                        <circle r={7} />
                        <text textAnchor="middle" dy="3.5">
                          +
                        </text>
                      </g>
                    )}
                  </g>
                );
              })}
              {gesture?.type === "link" && placed.get(gesture.id) && (
                <line
                  className="link-preview"
                  x1={placed.get(gesture.id)!.x + NODE_W / 2}
                  y1={placed.get(gesture.id)!.y + NODE_H}
                  x2={gesture.x}
                  y2={gesture.y}
                  markerEnd="url(#arrow-hi)"
                />
              )}
            </svg>
            {popover && (
              <DependencyPopover
                popover={popover}
                spec={spec}
                onSet={(protocol) => {
                  setDependency(popover.from, popover.to, protocol);
                  setPopover(null);
                }}
                onClose={() => setPopover(null)}
              />
            )}
          </div>
          <p className="hint">
            Click a component, a layer name, or a context outline for details, and ⤵ to drill into a component's code. Dashed boxes are planned.
            {editable &&
              " To edit: drag a component to another layer, drag its + handle onto another component to add a dependency, click an arrow to change or remove it, and double-click a component to edit it or a layer to add one. Use a layer's ▲ ▼ buttons to move it up or down. Unproposed draft changes are marked."}
          </p>
        </Section>
      </div>
      <aside className="detail">
        <Inspector {...{ sel, tech, spec, report, go, repo, branch }} />
      </aside>
    </div>
  );
}
