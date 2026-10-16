import type { CodeFunc, CodeMethod, CodeModel, CodePackage, CodeType } from "../../types";

/** A function, method, or type of the component. */
export interface Sym {
  id: string;
  kind: "type" | "method" | "func";
  name: string;
  pkg: CodePackage;
  type?: CodeType;
  method?: CodeMethod;
  func?: CodeFunc;
}

/** Splits a symbol id, "dir#Name" or "dir#Type.Method". */
export const splitId = (id: string) => {
  const [dir, name = ""] = id.split("#");
  return { dir, name };
};

/** The architecture route of a drill-down level. */
export function codeItem(component: string, dir?: string, symbol?: string) {
  let item = `${component}/code`;
  if (dir) item += `/${dir}`;
  if (symbol) item += `/~/${symbol}`;
  return item;
}

/** Indexes a component's code model for the drill-down views. */
export class CodeIndex {
  readonly symbols = new Map<string, Sym>();
  readonly packages = new Map<string, CodePackage>();
  private names = new Map<string, string>();
  private components = new Map<string, string>();
  private out = new Map<string, string[]>();
  private in = new Map<string, string[]>();
  private realizes = new Map<string, string[]>();
  private realizedBy = new Map<string, string[]>();
  private rpcs = new Set<string>();

  constructor(readonly model: CodeModel) {
    for (const p of model.packages) {
      this.packages.set(p.dir, p);
      this.names.set(p.dir, p.name);
      this.components.set(p.dir, model.component);
      for (const t of p.types) {
        this.symbols.set(t.id, { id: t.id, kind: "type", name: t.name, pkg: p, type: t });
        for (const m of t.methods) this.symbols.set(m.id, { id: m.id, kind: "method", name: `${t.name}.${m.name}`, pkg: p, type: t, method: m });
      }
      for (const f of p.funcs) this.symbols.set(f.id, { id: f.id, kind: "func", name: f.name, pkg: p, func: f });
      for (const r of p.relations) {
        if (r.kind !== "implements") continue;
        push(this.realizes, r.from, r.to);
        push(this.realizedBy, r.to, r.from);
      }
    }
    for (const n of model.neighbors) {
      this.names.set(n.dir, n.name);
      if (n.component) this.components.set(n.dir, n.component);
    }
    for (const d of model.dependents) if (d.component) this.components.set(d.from, d.component);
    for (const s of model.stubs) {
      this.names.set(s.dir, s.package);
      if (s.component) this.components.set(s.dir, s.component);
    }
    for (const [dir, d] of Object.entries(model.dirs ?? {})) {
      if (!this.names.has(dir)) this.names.set(dir, d.name);
      if (d.component && !this.components.has(dir)) this.components.set(dir, d.component);
    }
    for (const c of model.calls) {
      push(this.out, c.from, c.to);
      push(this.in, c.to, c.from);
      if (c.kind === "rpc") this.rpcs.add(`${c.from}>${c.to}`);
    }
  }

  /** "pkg.Type.Method" for a symbol id, wherever it is. */
  label(id: string) {
    const { dir, name } = splitId(id);
    return `${this.names.get(dir) ?? dir.split("/").pop()}.${name}`;
  }

  /** The component a package directory belongs to, when known. */
  componentOf(dir: string) {
    return this.components.get(dir);
  }

  /** The drill-down route of a symbol, when its component is known. */
  itemFor(id: string) {
    const { dir, name } = splitId(id);
    const c = this.componentOf(dir);
    return c ? codeItem(c, dir, name) : undefined;
  }

  /** Whether a call goes over gRPC, from a client method to a server method. */
  isRPC(from: string, to: string) {
    return this.rpcs.has(`${from}>${to}`);
  }

  callees(id: string) {
    return unique(this.out.get(id) ?? []);
  }

  /**
   * The callers of a symbol, including calls through an interface method it
   * implements, which are marked with that interface method.
   */
  callers(id: string): { id: string; via?: string }[] {
    const out: { id: string; via?: string }[] = (this.in.get(id) ?? []).map((c) => ({ id: c }));
    const s = this.symbols.get(id);
    if (s?.kind === "method" && s.type && s.method) {
      for (const iface of this.realizes.get(s.type.id) ?? []) {
        const via = `${iface}.${s.method.name}`;
        for (const c of this.in.get(via) ?? []) out.push({ id: c, via });
      }
    }
    return out.filter((c, i) => out.findIndex((o) => o.id === c.id) === i);
  }

  /** The methods that implement an interface method. */
  implementations(id: string) {
    const { dir, name } = splitId(id);
    const [iface, method] = name.split(".");
    if (!method) return [];
    return (this.realizedBy.get(`${dir}#${iface}`) ?? [])
      .map((t) => `${t}.${method}`)
      .filter((m) => this.symbols.has(m));
  }

  /** The interfaces a type implements. */
  interfaces(typeId: string) {
    return this.realizes.get(typeId) ?? [];
  }

  /** The types that implement an interface. */
  implementers(typeId: string) {
    return this.realizedBy.get(typeId) ?? [];
  }
}

function push(m: Map<string, string[]>, k: string, v: string) {
  const l = m.get(k);
  if (l) l.push(v);
  else m.set(k, [v]);
}

const unique = (l: string[]) => [...new Set(l)];

/** A package directory without the leading "backend/", for labels. */
export const shortDir = (dir: string) => dir.replace(/^backend\//, "");
