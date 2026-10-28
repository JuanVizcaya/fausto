import type { Kind, Schema, Spec } from "./types";

/** Where each kind's item schema lives: schema file and path inside it. */
const locations: Record<Kind, [file: string, ...path: string[]]> = {
  component: ["system", "components"],
  layer: ["system", "layers"],
  technology: ["tech", "technologies"],
  principle: ["principles", "principles"],
  check: ["checks", "checks"],
  domain: ["domains", "domains"],
  term: ["domains", "glossary"],
  agent: ["agent"],
  decision: ["decision"],
  milestone: ["roadmap", "milestones"],
  requirement: ["requirements", "requirements"],
  risk: ["risks", "risks"],
  event: ["events", "events"],
};

export const kindLabel: Record<Kind, string> = {
  component: "component",
  layer: "layer",
  technology: "technology",
  principle: "principle",
  check: "project check",
  domain: "domain",
  term: "glossary term",
  agent: "agent",
  decision: "decision",
  milestone: "milestone",
  requirement: "requirement",
  risk: "risk",
  event: "event",
};

/** The field that identifies items of a kind. */
export const idField = (kind: Kind) => (kind === "term" ? "term" : "id");

/** Returns the schema for one item of kind and the root it resolves
 * $refs against. */
export function itemSchema(schemas: Record<string, Schema>, kind: Kind): { schema: Schema; root: Schema } | null {
  const [file, ...path] = locations[kind];
  const root = schemas[file];
  if (!root) return null;
  let s: Schema | undefined = root;
  for (const key of path) s = s?.properties?.[key]?.items;
  if (!s) return null;
  if (kind === "decision" && s.properties) {
    // Decision ids are assigned by the server and never change.
    const { id: _id, ...properties } = s.properties;
    s = { ...s, properties, required: s.required?.filter((r) => r !== "id") };
  }
  return { schema: s, root };
}

export function resolve(schema: Schema, root: Schema): Schema {
  let s = schema;
  for (let i = 0; s.$ref && i < 10; i++) {
    const name = s.$ref.replace(/^#\/\$defs\//, "");
    const target = root.$defs?.[name];
    if (!target) break;
    const { $ref: _ref, ...rest } = s;
    s = { ...target, ...rest };
  }
  return s;
}

/** Suggested values for fields that reference other spec items. */
export function suggestions(spec: Spec): Record<string, string[]> {
  const components = spec.system.components.map((c) => c.id);
  const layers = spec.system.layers.map((l) => l.id);
  return {
    owner: spec.agents.map((a) => a.id),
    layer: layers,
    may_depend_on: layers,
    tech: spec.tech.technologies.map((t) => t.id),
    target: components,
    components,
    affects: components,
    supersedes: spec.decisions.map((d) => d.id),
    decisions: spec.decisions.map((d) => d.id),
    layers: spec.system.layers.map((l) => l.id),
    milestones: spec.roadmap.milestones.map((m) => m.id),
    producer: components,
    consumers: components,
    requirements: spec.requirements.requirements.map((r) => r.id),
    verified_by: [
      ...spec.runtimeRules.map((r) => `runtime/${r}`),
      "schema",
      "services",
      "layers",
      "db-ownership",
      "contracts",
      "ownership",
    ],
    // Only milestones use a plain depends_on list; components use objects.
    depends_on: spec.roadmap.milestones.map((m) => m.id),
  };
}

/** Removes empty optional values and orders keys as the schema lists them,
 * which is the order they are written to YAML. */
export function clean(value: unknown, schema: Schema, root: Schema, required = true): unknown {
  const s = resolve(schema, root);
  if (s.type === "object" || s.properties) {
    if (typeof value !== "object" || value === null || Array.isArray(value)) return undefined;
    const src = value as Record<string, unknown>;
    const out: Record<string, unknown> = {};
    for (const [key, sub] of Object.entries(s.properties ?? {})) {
      const v = clean(src[key], sub, root, s.required?.includes(key) ?? false);
      if (v !== undefined) out[key] = v;
    }
    return Object.keys(out).length || required ? out : undefined;
  }
  if (s.type === "array") {
    if (!Array.isArray(value)) return required ? [] : undefined;
    const items = value.map((v) => clean(v, s.items ?? {}, root)).filter((v) => v !== undefined && v !== "");
    return items.length || required ? items : undefined;
  }
  if (typeof value === "boolean") return value || required ? value : undefined;
  if (typeof value === "string") {
    const t = value.trim();
    return t || required ? t : undefined;
  }
  return value ?? undefined;
}
