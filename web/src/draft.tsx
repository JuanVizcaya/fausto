import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { idField } from "./schema";
import type { Change, Kind, Spec } from "./types";
import { currentProject } from "./api";

/** A pending edit plus a stable key: kind and the item's original id, or
 * its new id for items that don't exist yet. */
export interface DraftChange extends Change {
  key: string;
  label: string;
}

interface Draft {
  changes: DraftChange[];
  put: (change: DraftChange) => void;
  remove: (key: string) => void;
  clear: () => void;
  /** The change to the item with this id, matching its original id or, for
   * added and renamed items, its id in the draft. */
  find: (kind: Kind, id: string) => DraftChange | undefined;
}

const DraftContext = createContext<Draft | null>(null);
// Drafts are per project: each one's edits only apply to its spec.
const storageKey = "fausto-draft" + (currentProject ? ":" + currentProject : "");

function loadStored(): DraftChange[] {
  try {
    const raw = localStorage.getItem(storageKey);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export function DraftProvider({ children }: { children: ReactNode }) {
  const [changes, setChanges] = useState<DraftChange[]>(loadStored);

  useEffect(() => {
    try {
      localStorage.setItem(storageKey, JSON.stringify(changes));
    } catch {
      // The draft still works for this page view.
    }
  }, [changes]);

  const put = useCallback((change: DraftChange) => {
    setChanges((cs) => {
      const i = cs.findIndex((c) => c.key === change.key);
      if (i < 0) return [...cs, change];
      // Deleting an item that only exists in the draft drops it entirely.
      if (change.value === null && !cs[i].id) return cs.filter((_, j) => j !== i);
      return cs.map((c, j) => (j === i ? change : c));
    });
  }, []);
  const remove = useCallback((key: string) => setChanges((cs) => cs.filter((c) => c.key !== key)), []);
  const clear = useCallback(() => setChanges([]), []);
  const find = useCallback(
    (kind: Kind, id: string) =>
      changes.find((c) => c.kind === kind && c.id === id) ?? changes.find((c) => c.kind === kind && c.value?.[idField(kind)] === id),
    [changes],
  );

  const value = useMemo(() => ({ changes, put, remove, clear, find }), [changes, put, remove, clear, find]);
  return <DraftContext.Provider value={value}>{children}</DraftContext.Provider>;
}

export function useDraft() {
  const d = useContext(DraftContext);
  if (!d) throw new Error("useDraft outside DraftProvider");
  return d;
}

/** The list in spec that holds items of kind. */
function collection(s: Spec, kind: Kind): Record<string, unknown>[] {
  const lists: Record<Kind, () => unknown[]> = {
    component: () => s.system.components,
    layer: () => s.system.layers,
    technology: () => s.tech.technologies,
    principle: () => s.principles.principles,
    domain: () => s.domains.domains,
    term: () => (s.domains.glossary ??= []),
    agent: () => s.agents,
    decision: () => s.decisions,
    milestone: () => s.roadmap.milestones,
    requirement: () => s.requirements.requirements,
    risk: () => s.risks.risks,
    event: () => s.events.events,
  };
  return lists[kind]() as Record<string, unknown>[];
}

export type DraftMark = "added" | "edited" | "deleted";

/** Returns spec as it would be with changes applied, and how each changed
 * item was changed, keyed by "kind:id" (its id in the draft). With
 * keepDeleted, deleted items stay in the spec so views can show them. */
export function applyDraft(spec: Spec, changes: Change[], keepDeleted = false) {
  const out: Spec = structuredClone(spec);
  const marks = new Map<string, DraftMark>();
  for (const c of changes) {
    const list = collection(out, c.kind);
    const key = idField(c.kind);
    if (c.order) {
      list.splice(0, list.length, ...reorder(list, c.order, (x) => String(x[key])));
      continue;
    }
    const i = c.id === undefined ? -1 : list.findIndex((x) => x[key] === c.id);
    if (c.value === null) {
      if (i < 0) continue;
      marks.set(`${c.kind}:${c.id}`, "deleted");
      if (!keepDeleted) list.splice(i, 1);
      continue;
    }
    // Agents and decisions carry fields the form doesn't edit (file, body).
    const keep = (c.kind === "agent" || c.kind === "decision") && i >= 0 ? list[i] : {};
    const item: Record<string, unknown> = { ...keep, ...c.value, ...(c.body !== undefined ? { body: c.body } : {}) };
    if (i < 0) list.push(item);
    else list[i] = item;
    marks.set(`${c.kind}:${String(item[key] ?? c.id)}`, i < 0 ? "added" : "edited");
  }
  return { spec: out, marks };
}

/** The draft key of the change that reorders the list of kind. */
export const orderKey = (kind: Kind) => `${kind}:order`;

/** list with the items order names, in that order, in the places they
 * held; the server applies an order change the same way. */
export function reorder<T extends object>(list: T[], order: string[], id: (x: T) => string): T[] {
  const byId = new Map(list.map((x) => [id(x), x]));
  const named = [...new Set(order)].filter((o) => byId.has(o)).map((o) => byId.get(o)!);
  const names = new Set(named);
  let next = 0;
  return list.map((x) => (names.has(x) ? named[next++] : x));
}
