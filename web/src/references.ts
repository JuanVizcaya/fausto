import type { Kind, Spec } from "./types";

type Value = Record<string, unknown>;

/** One spec item that refers to a component or layer, and how to update it
 * when that component or layer is renamed (to is the new id) or deleted
 * (to is null). update returns null to delete the referring item. */
export interface Reference {
  kind: Kind;
  id: string;
  /** What the reference is, e.g. "depends on it". */
  what: string;
  /** Deleting the target can't be cleaned up automatically. */
  blocks?: boolean;
  /** Deleting the target deletes this item too. */
  cascades?: boolean;
  update: (v: Value, to: string | null) => Value | null;
}

/** Renames from to `to` in v[field], or removes it when to is null. Empty
 * optional lists are dropped. */
function relist(v: Value, field: string, from: string, to: string | null, required = false): Value {
  const list = ((v[field] as string[] | undefined) ?? []).flatMap((x) => (x === from ? (to ? [to] : []) : [x]));
  const out = { ...v, [field]: [...new Set(list)] };
  if (!list.length && !required) delete out[field];
  return out;
}

/** Same as relist for a field inside a nested object, e.g. applies_to. */
function relistIn(v: Value, obj: string, field: string, from: string, to: string | null): Value {
  const inner = relist((v[obj] as Value | undefined) ?? {}, field, from, to);
  const out = { ...v, [obj]: inner };
  if (!Object.keys(inner).length) delete out[obj];
  return out;
}

const has = (list: string[] | undefined, id: string) => !!list?.includes(id);

/** Lists the items in spec that refer to the component or layer id. */
export function references(spec: Spec, kind: Kind, id: string): Reference[] {
  const out: Reference[] = [];
  if (kind === "component") {
    for (const c of spec.system.components) {
      if (c.id !== id && c.depends_on?.some((d) => d.target === id)) {
        out.push({
          kind: "component",
          id: c.id,
          what: "depends on it",
          update: (v, to) => {
            const deps = ((v.depends_on as { target: string }[] | undefined) ?? []).flatMap((d) =>
              d.target === id ? (to ? [{ ...d, target: to }] : []) : [d],
            );
            const next: Value = { ...v, depends_on: deps };
            if (!deps.length) delete next.depends_on;
            return next;
          },
        });
      }
    }
    for (const d of spec.domains.domains) {
      if (has(d.components, id)) out.push({ kind: "domain", id: d.id, what: "lists it", update: (v, to) => relist(v, "components", id, to) });
    }
    for (const m of spec.roadmap.milestones) {
      if (has(m.components, id)) out.push({ kind: "milestone", id: m.id, what: "lists it", update: (v, to) => relist(v, "components", id, to) });
    }
    for (const r of spec.risks.risks) {
      if (has(r.components, id)) out.push({ kind: "risk", id: r.id, what: "lists it", update: (v, to) => relist(v, "components", id, to) });
    }
    for (const r of spec.requirements.requirements) {
      if (has(r.applies_to?.components, id)) {
        out.push({ kind: "requirement", id: r.id, what: "applies to it", update: (v, to) => relistIn(v, "applies_to", "components", id, to) });
      }
    }
    for (const d of spec.decisions) {
      if (has(d.affects, id)) out.push({ kind: "decision", id: d.id, what: "affects it", update: (v, to) => relist(v, "affects", id, to) });
    }
    for (const e of spec.events.events) {
      if (e.producer === id) {
        out.push({
          kind: "event",
          id: e.id,
          what: "is published by it",
          cascades: true,
          update: (v, to) => (to ? { ...v, producer: to } : null),
        });
      } else if (has(e.consumers, id)) {
        out.push({ kind: "event", id: e.id, what: "is consumed by it", update: (v, to) => relist(v, "consumers", id, to) });
      }
    }
  }
  if (kind === "layer") {
    for (const c of spec.system.components) {
      if (c.layer === id) {
        out.push({ kind: "component", id: c.id, what: "is in it", blocks: true, update: (v, to) => (to ? { ...v, layer: to } : v) });
      }
    }
    for (const l of spec.system.layers) {
      if (l.id !== id && has(l.may_depend_on, id)) {
        out.push({ kind: "layer", id: l.id, what: "may depend on it", update: (v, to) => relist(v, "may_depend_on", id, to, true) });
      }
    }
    for (const r of spec.requirements.requirements) {
      if (has(r.applies_to?.layers, id)) {
        out.push({ kind: "requirement", id: r.id, what: "applies to it", update: (v, to) => relistIn(v, "applies_to", "layers", id, to) });
      }
    }
  }
  return out;
}
