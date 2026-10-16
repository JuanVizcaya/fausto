import type { MouseEvent } from "react";
import type { CodePackage, CodeRelation, CodeStub, CodeType } from "../../types";
import { Canvas, clip, layoutGraph, textWidth, titleWidth, type GEdge, type GNode } from "./diagram";
import { CodeIndex, codeItem } from "./model";

const LINE = 15;
const MAX_LINES = 12;

/** Shortens a Go signature for a class box: no package qualifiers. */
export const shortSig = (sig: string) => sig.replace(/\b[a-z]\w*\./g, "");

const vis = (exported: boolean) => (exported ? "+" : "-");

interface Row {
  text: string;
  full: string;
  target?: string;
}

interface ClassBox extends GNode {
  stereotype?: string;
  title: string;
  fields: Row[];
  methods: Row[];
  target?: string;
  stub?: boolean;
  /** Symbol ids this box stands for, to highlight the selected one. */
  ids: string[];
}

function rows(list: Row[]) {
  return list.length > MAX_LINES ? [...list.slice(0, MAX_LINES - 1), { text: `… ${list.length - MAX_LINES + 1} more`, full: "" }] : list;
}

function typeBox(component: string, p: CodePackage, t: CodeType): ClassBox {
  const fields: Row[] =
    t.kind === "named" || t.kind === "alias"
      ? [{ text: `= ${t.underlying ?? ""}`, full: t.underlying ?? "" }]
      : t.fields.map((f) => {
          const text = f.embedded ? `«embed» ${f.type}` : `${vis(f.exported)} ${f.name}: ${f.type}`;
          return { text: clip(text, 52), full: text };
        });
  const methods: Row[] = t.methods.map((m) => {
    const text = `${vis(m.exported)} ${m.name}${shortSig(m.sig)}`;
    return { text: clip(text, 52), full: `${m.name}${m.sig}`, target: codeItem(component, p.dir, `${t.name}.${m.name}`) };
  });
  const stereotype = t.kind === "interface" ? "«interface»" : t.kind === "struct" ? undefined : t.kind === "alias" ? "«alias»" : "«type»";
  return sized({
    id: t.id,
    stereotype,
    title: t.name,
    fields: rows(fields),
    methods: rows(methods),
    target: codeItem(component, p.dir, t.name),
    ids: [t.id, ...t.methods.map((m) => m.id)],
  });
}

function stubBox(s: CodeStub): ClassBox {
  return sized({
    id: s.id,
    stereotype: s.kind === "interface" ? "«interface»" : undefined,
    title: `${s.package}.${s.name}`,
    fields: [],
    methods: rows((s.methods ?? []).map((m) => ({ text: `+ ${m}()`, full: m }))),
    target: s.component ? codeItem(s.component, s.dir, s.name) : undefined,
    stub: true,
    ids: [s.id],
  });
}

function sized(b: Omit<ClassBox, "width" | "height">): ClassBox {
  const lines = [...b.fields.map((f) => f.text), ...b.methods.map((m) => m.text)];
  const width = Math.min(420, Math.max(150, titleWidth(b.title) + 24, ...lines.map((l) => textWidth(l) + 22)));
  return { ...b, width, height: header(b) + compartment(b.fields) + compartment(b.methods) };
}

const header = (b: Pick<ClassBox, "stereotype">) => (b.stereotype ? 40 : 28);
const compartment = (r: Row[]) => (r.length ? r.length * LINE + 10 : 10);

/** Relations point from the specific to the general, which goes on top. */
const upward = (r: CodeRelation) => r.kind === "implements" || r.kind === "embeds";

/**
 * Level 3: a UML class diagram of one package: its types with their fields
 * and methods, its functions, and the types of other packages they relate to.
 */
export function PackageLevel({
  component,
  pkg,
  code,
  selected,
  nav,
}: {
  component: string;
  pkg: CodePackage;
  code: CodeIndex;
  selected?: string;
  nav: (item: string) => void;
}) {
  const boxes: ClassBox[] = pkg.types.map((t) => typeBox(component, pkg, t));
  const stubs = new Map(code.model.stubs.map((s) => [s.id, s]));
  const ids = new Set(boxes.map((b) => b.id));
  // Types of the component's other packages appear as stubs too.
  for (const r of pkg.relations) {
    if (ids.has(r.to)) continue;
    ids.add(r.to);
    const s = stubs.get(r.to);
    const other = code.symbols.get(r.to);
    if (s) boxes.push(stubBox(s));
    else if (other?.type)
      boxes.push(
        stubBox({
          id: other.id,
          name: other.type.name,
          package: other.pkg.name,
          dir: other.pkg.dir,
          component,
          kind: other.type.kind,
          methods: other.type.methods.filter((m) => m.exported).map((m) => m.name),
        }),
      );
  }
  if (pkg.funcs.length) {
    boxes.push(
      sized({
        id: `${pkg.dir}#`,
        stereotype: "«functions»",
        title: pkg.name,
        fields: [],
        methods: rows(
          pkg.funcs.map((f) => {
            const text = `${vis(f.exported)} ${f.name}${shortSig(f.sig)}`;
            return { text: clip(text, 52), full: `${f.name}${f.sig}`, target: codeItem(component, pkg.dir, f.name) };
          }),
        ),
        ids: pkg.funcs.map((f) => f.id),
      }),
    );
  }
  const edges: (GEdge & { rel: CodeRelation })[] = pkg.relations
    .filter((r) => ids.has(r.from) && ids.has(r.to))
    .map((r, i) => ({ id: `r${i}`, from: upward(r) ? r.to : r.from, to: upward(r) ? r.from : r.to, label: r.label, rel: r }));
  const laid = layoutGraph(boxes, edges, { nodesep: 40, ranksep: 70 });

  return (
    <>
      <Canvas width={laid.width} height={laid.height} label={`Class diagram of ${pkg.dir}`}>
        {edges.map((e) => {
          const p = laid.edges.get(e.id);
          if (!p) return null;
          const k = e.rel.kind;
          const hi = selected && (selected === e.rel.from || selected === e.rel.to);
          // Layout runs general → specific for upward relations, so their
          // head is at the path's start.
          const head = k === "implements" || k === "embeds" ? "url(#uml-triangle)" : "url(#uml-open)";
          return (
            <g key={e.id} className={`rel rel-${k}${hi ? " hi" : ""}`}>
              <path
                d={p.d}
                strokeDasharray={k === "implements" || k === "uses" ? "6 4" : undefined}
                markerStart={upward(e.rel) ? head : k === "has" ? "url(#uml-diamond)" : undefined}
                markerEnd={upward(e.rel) ? undefined : head}
              />
              {e.label && (
                <text x={p.mid.x + 6} y={p.mid.y - 4} className="edge-label">
                  {e.label}
                </text>
              )}
            </g>
          );
        })}
        {boxes.map((b) => {
          const at = laid.boxes.get(b.id);
          if (!at) return null;
          const isSel = !!selected && b.ids.includes(selected);
          const fy = header(b);
          const my = fy + compartment(b.fields);
          const open = (target?: string) => (e: MouseEvent) => {
            e.stopPropagation();
            if (target) nav(target);
          };
          return (
            <g key={b.id} className={`uml${b.stub ? " stub" : ""}${isSel ? " selected" : ""}`} transform={`translate(${at.x} ${at.y})`}>
              <rect width={at.width} height={at.height} rx={4} className="uml-box" />
              <g
                className={b.target ? "uml-head-row clickable" : "uml-head-row"}
                onClick={open(b.target)}
                role={b.target ? "button" : undefined}
                tabIndex={b.target ? 0 : undefined}
                onKeyDown={(e) => b.target && e.key === "Enter" && nav(b.target)}
                aria-label={b.title}
              >
                <rect width={at.width} height={fy} className="uml-hit" />
                {b.stereotype && (
                  <text x={at.width / 2} y={15} textAnchor="middle" className="stereotype">
                    {b.stereotype}
                  </text>
                )}
                <text x={at.width / 2} y={fy - 10} textAnchor="middle" className={`uml-title${!b.stub && !b.stereotype?.includes("functions") && /^[a-z_]/.test(b.title) ? " unexported" : ""}`}>
                  {b.title}
                </text>
              </g>
              <line x1={0} x2={at.width} y1={fy} y2={fy} className="uml-rule" />
              {b.fields.map((f, i) => (
                <text key={i} x={10} y={fy + 16 + i * LINE} className="uml-row">
                  <title>{f.full}</title>
                  {f.text}
                </text>
              ))}
              <line x1={0} x2={at.width} y1={my} y2={my} className="uml-rule" />
              {b.methods.map((m, i) => (
                <g
                  key={i}
                  className={`uml-method${m.target ? " clickable" : ""}${selected && m.target && selected === idOf(m.target) ? " current" : ""}`}
                  onClick={open(m.target)}
                >
                  <rect x={1} y={my + 4 + i * LINE} width={at.width - 2} height={LINE} className="uml-hit" />
                  <text x={10} y={my + 16 + i * LINE} className="uml-row">
                    <title>{m.full}</title>
                    {m.text}
                  </text>
                </g>
              ))}
            </g>
          );
        })}
      </Canvas>
      <Legend />
    </>
  );
}

/** The symbol id a drill-down route points at. */
function idOf(item: string) {
  const [, rest] = item.split("/code/");
  const [dir, sym] = rest.split("/~/");
  return `${dir}#${sym}`;
}

function Legend() {
  return (
    <div className="uml-legend" aria-label="Legend">
      <span>
        <svg width="46" height="14">
          <line x1="2" y1="7" x2="34" y2="7" className="legend-line dashed" />
          <path d="M 32 1 L 44 7 L 32 13 z" className="uml-head hollow" />
        </svg>
        implements
      </span>
      <span>
        <svg width="46" height="14">
          <line x1="2" y1="7" x2="34" y2="7" className="legend-line" />
          <path d="M 32 1 L 44 7 L 32 13 z" className="uml-head hollow" />
        </svg>
        embeds
      </span>
      <span>
        <svg width="46" height="14">
          <path d="M 2 7 L 9 3 L 16 7 L 9 11 z" className="uml-head filled" />
          <line x1="16" y1="7" x2="44" y2="7" className="legend-line" />
        </svg>
        has (field)
      </span>
      <span>
        <svg width="46" height="14">
          <line x1="2" y1="7" x2="42" y2="7" className="legend-line dashed" />
          <path d="M 34 2 L 43 7 L 34 12" className="uml-head open" />
        </svg>
        uses (in a signature)
      </span>
      <span className="muted">+ exported · − unexported. Click a type or method to see how it's called.</span>
    </div>
  );
}
