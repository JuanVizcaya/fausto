import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { applyDraft, useDraft, type DraftChange } from "./draft";
import { references, type Reference } from "./references";
import { clean, idField, itemSchema, kindLabel, suggestions } from "./schema";
import { SchemaForm } from "./SchemaForm";
import type { Kind, Schema, Spec } from "./types";

export interface EditTarget {
  kind: Kind;
  /** Existing item to edit; omitted to add a new one. */
  id?: string;
  /** Draft entry to reopen. */
  draftKey?: string;
  /** Starting values for a new item. */
  initial?: Record<string, unknown>;
  /** Runs after the item is saved to the draft. */
  onSaved?: (value: Record<string, unknown>) => void;
}

interface Editor {
  editable: boolean;
  edit: (target: EditTarget) => void;
}

const EditorContext = createContext<Editor>({ editable: false, edit: () => {} });
export const useEditor = () => useContext(EditorContext);

/** Returns the spec's current value for an item, as the form edits it. */
export function currentValue(spec: Spec, kind: Kind, id: string): { value: Record<string, unknown>; body?: string } | null {
  const strip = <T extends object>(o: T, ...keys: string[]) =>
    Object.fromEntries(Object.entries(o).filter(([k]) => !keys.includes(k))) as Record<string, unknown>;
  switch (kind) {
    case "component": {
      const c = spec.system.components.find((x) => x.id === id);
      return c ? { value: { ...c } } : null;
    }
    case "layer": {
      const l = spec.system.layers.find((x) => x.id === id);
      return l ? { value: { ...l } } : null;
    }
    case "technology": {
      const t = spec.tech.technologies.find((x) => x.id === id);
      return t ? { value: { ...t } } : null;
    }
    case "principle": {
      const p = spec.principles.principles.find((x) => x.id === id);
      return p ? { value: { ...p } } : null;
    }
    case "check": {
      const c = spec.checks.checks?.find((x) => x.id === id);
      return c ? { value: { ...c } } : null;
    }
    case "domain": {
      const d = spec.domains.domains.find((x) => x.id === id);
      return d ? { value: { ...d } } : null;
    }
    case "term": {
      const t = spec.domains.glossary?.find((x) => x.term === id);
      return t ? { value: { ...t } } : null;
    }
    case "agent": {
      const a = spec.agents.find((x) => x.id === id);
      return a ? { value: strip(a, "file") } : null;
    }
    case "event": {
      const e = spec.events.events.find((x) => x.id === id);
      return e ? { value: { ...e } } : null;
    }
    case "risk": {
      const r = spec.risks.risks.find((x) => x.id === id);
      return r ? { value: { ...r } } : null;
    }
    case "requirement": {
      const r = spec.requirements.requirements.find((x) => x.id === id);
      return r ? { value: { ...r } } : null;
    }
    case "milestone": {
      const m = spec.roadmap.milestones.find((x) => x.id === id);
      return m ? { value: { ...m } } : null;
    }
    case "decision": {
      const d = spec.decisions.find((x) => x.id === id);
      return d ? { value: strip(d, "file", "body", "id"), body: d.body } : null;
    }
  }
}

type Draft = ReturnType<typeof useDraft>;

/** Records a draft change to the item kind:id, computed by fn from its value
 * in spec (the spec with the draft applied). fn returning null deletes it.
 * Builds on the item's existing draft change, if any. */
export function putUpdate(
  draft: Draft,
  spec: Spec,
  kind: Kind,
  id: string,
  fn: (v: Record<string, unknown>) => Record<string, unknown> | null,
  /** The spec without the draft: an edit that restores the item drops its change. */
  saved?: Spec,
) {
  const cur = currentValue(spec, kind, id);
  if (!cur) return;
  const existing = draft.find(kind, id);
  const next = fn(structuredClone(cur.value));
  const key = existing?.key ?? `${kind}:${id}`;
  const original = existing ? existing.id : id;
  const before = saved && original ? currentValue(saved, kind, original) : null;
  if (next && before && stableJSON(next) === stableJSON(before.value)) {
    if (existing) draft.remove(existing.key);
    return;
  }
  if (next === null) {
    draft.put({ key, kind, id: original, value: null, label: `Delete ${kindLabel[kind]} ${original ?? id}` });
    return;
  }
  draft.put({
    key,
    kind,
    id: original,
    value: next,
    body: cur.body,
    label: existing?.label ?? `Edit ${kindLabel[kind]} ${id}`,
  });
}

/** JSON with object keys sorted, for comparing values. */
function stableJSON(v: unknown): string {
  return JSON.stringify(v, (_, x) =>
    x && typeof x === "object" && !Array.isArray(x) ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => a.localeCompare(b))) : x,
  );
}

/** Kinds whose renames and deletions are checked for references. */
const referenced: Kind[] = ["component", "layer"];

const decisionTemplate = "## Context\n\n\n## Decision\n\n\n## Consequences\n";

export function EditorProvider({
  spec,
  schemas,
  editable,
  children,
}: {
  spec: Spec | null;
  schemas: Record<string, Schema> | null;
  editable: boolean;
  children: ReactNode;
}) {
  const [target, setTarget] = useState<EditTarget | null>(null);
  const edit = useCallback((t: EditTarget) => setTarget(t), []);
  const value = useMemo(() => ({ editable: editable && !!schemas, edit }), [editable, schemas, edit]);
  return (
    <EditorContext.Provider value={value}>
      {children}
      {target && spec && schemas && (
        <EditDialog key={JSON.stringify(target)} target={target} spec={spec} schemas={schemas} onClose={() => setTarget(null)} />
      )}
    </EditorContext.Provider>
  );
}

interface Pending {
  mode: "rename" | "delete";
  from: string;
  to: string | null;
  refs: Reference[];
  change?: DraftChange;
}

function EditDialog({ target, spec, schemas, onClose }: { target: EditTarget; spec: Spec; schemas: Record<string, Schema>; onClose: () => void }) {
  const draft = useDraft();
  const [pending, setPending] = useState<Pending | null>(null);
  const found = itemSchema(schemas, target.kind);
  const existing = target.draftKey
    ? draft.changes.find((c) => c.key === target.draftKey)
    : target.id
      ? draft.find(target.kind, target.id)
      : undefined;
  const original = target.id ? currentValue(spec, target.kind, target.id) : null;
  const itemId = target.id ?? existing?.id;
  const [value, setValue] = useState<unknown>(
    existing?.value ?? original?.value ?? target.initial ?? (target.kind === "decision" ? { status: "proposed" } : {}),
  );
  const [body, setBody] = useState(existing?.body ?? original?.body ?? (target.kind === "decision" ? decisionTemplate : ""));
  const dialog = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    dialog.current?.querySelector<HTMLElement>("input, select, textarea")?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  if (!found) return null;
  const { schema, root } = found;
  const label = kindLabel[target.kind];
  const isNew = !itemId;
  const deleted = existing?.value === null;
  // The item's id with the draft applied, which references point at.
  const currentId = (existing?.value?.[idField(target.kind)] as string | undefined) ?? itemId;
  const refsTo = (id: string | undefined) =>
    id && referenced.includes(target.kind) ? references(applyDraft(spec, draft.changes).spec, target.kind, id) : [];

  /** Updates every reference to the item, then closes. */
  const finish = (p: Pending | null, saved?: Record<string, unknown>) => {
    if (p) {
      const effective = applyDraft(spec, draft.changes).spec;
      for (const r of p.refs) putUpdate(draft, effective, r.kind, r.id, (v) => r.update(v, p.to));
    }
    if (saved) target.onSaved?.(saved);
    onClose();
  };

  const save = () => {
    const cleaned = clean(value, schema, root) as Record<string, unknown>;
    const newId = String(cleaned[idField(target.kind)] ?? cleaned.title ?? "");
    const key = existing?.key ?? (isNew ? `${target.kind}:new:${Date.now()}` : `${target.kind}:${itemId}`);
    const change: DraftChange = {
      key,
      kind: target.kind,
      id: itemId,
      value: cleaned,
      body: target.kind === "decision" ? body : undefined,
      label: isNew ? `Add ${label} ${newId}` : `Edit ${label} ${itemId}${newId && newId !== itemId && target.kind !== "decision" ? ` → ${newId}` : ""}`,
    };
    if (currentId && newId && newId !== currentId) {
      const refs = refsTo(currentId);
      if (refs.length) {
        setPending({ mode: "rename", from: currentId, to: newId, refs, change });
        return;
      }
    }
    draft.put(change);
    finish(null, cleaned);
  };
  const doRemove = () => {
    if (isNew) {
      if (existing) draft.remove(existing.key);
    } else {
      draft.put({ key: existing?.key ?? `${target.kind}:${itemId}`, kind: target.kind, id: itemId, value: null, label: `Delete ${label} ${itemId}` });
    }
  };
  const remove = () => {
    const refs = refsTo(currentId);
    if (refs.length) {
      setPending({ mode: "delete", from: currentId!, to: null, refs });
      return;
    }
    doRemove();
    onClose();
  };
  const confirm = () => {
    if (!pending) return;
    if (pending.mode === "rename") draft.put(pending.change!);
    else doRemove();
    finish(pending, pending.change?.value ?? undefined);
  };

  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog" role="dialog" aria-modal="true" aria-labelledby="edit-title" ref={dialog}>
        <header className="dialog-head">
          <h2 id="edit-title">
            {isNew ? `New ${label}` : `Edit ${label} `}
            {!isNew && <code className="code">{itemId}</code>}
          </h2>
          <button className="btn btn-ghost" onClick={onClose} aria-label="Close">
            ×
          </button>
        </header>
        <div className="dialog-body">
          {deleted && <div className="banner bad">This {label} is marked for deletion in your draft. Saving restores it with these values.</div>}
          <SchemaForm schema={schema} root={root} value={value} onChange={setValue} suggest={suggestions(spec)} />
          {target.kind === "decision" && (
            <div className="field">
              <label className="field-label" htmlFor="decision-body">
                Body (Markdown)
              </label>
              <textarea id="decision-body" className="mono" rows={12} value={body} onChange={(e) => setBody(e.target.value)} />
            </div>
          )}
        </div>
        {pending ? (
          <ReferencesPanel pending={pending} label={label} onBack={() => setPending(null)} onConfirm={confirm} />
        ) : (
        <footer className="dialog-foot">
          {(!isNew || existing) && (
            <button className="btn btn-danger" onClick={remove}>
              {isNew ? "Discard" : `Delete ${label}`}
            </button>
          )}
          <span className="spacer" />
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" onClick={save}>
            Save to draft
          </button>
        </footer>
        )}
      </div>
    </div>
  );
}

/** Lists what refers to an item being renamed or deleted, and offers to
 * update those references in the same draft. */
function ReferencesPanel({ pending, label, onBack, onConfirm }: { pending: Pending; label: string; onBack: () => void; onConfirm: () => void }) {
  const { mode, from, to, refs } = pending;
  const blocked = mode === "delete" && refs.some((r) => r.blocks);
  const shown = blocked ? refs.filter((r) => r.blocks) : refs;
  const n = refs.length;
  return (
    <footer className="dialog-foot refs-panel">
      <div className="refs-body">
        <p>
          {blocked ? (
            <>
              <strong>
                The {label} <code className="code">{from}</code> still has components.
              </strong>{" "}
              Move them to another {label} or delete them first.
            </>
          ) : mode === "delete" ? (
            <>
              <strong>
                {n} {n === 1 ? "item refers" : "items refer"} to <code className="code">{from}</code>.
              </strong>{" "}
              Deleting it also removes these references:
            </>
          ) : (
            <>
              <strong>
                {n} {n === 1 ? "item refers" : "items refer"} to <code className="code">{from}</code>.
              </strong>{" "}
              Renaming it to <code className="code">{to}</code> updates them:
            </>
          )}
        </p>
        <ul className="refs-list">
          {shown.map((r) => (
            <li key={`${r.kind}:${r.id}`}>
              {kindLabel[r.kind]} <code className="code">{r.id}</code> {r.what}
              {mode === "delete" && r.cascades && <span className="pill pill-bad">deleted too</span>}
            </li>
          ))}
        </ul>
      </div>
      <div className="refs-actions">
        <span className="spacer" />
        <button className="btn" onClick={onBack}>
          Back
        </button>
        {!blocked && (
          <button className={mode === "delete" ? "btn btn-danger" : "btn btn-primary"} onClick={onConfirm}>
            {mode === "delete" ? `Delete and clean up ${n}` : `Rename and update ${n}`}
          </button>
        )}
      </div>
    </footer>
  );
}

/** The dialog target for an item by its id in the draft: items added in the
 * draft reopen their entry; renamed ones open by their original id. */
export function targetFor(draft: Draft, kind: Kind, id: string): EditTarget {
  const pending = draft.find(kind, id);
  return pending && !pending.id ? { kind, draftKey: pending.key } : { kind, id: pending?.id ?? id };
}

/** A small Edit button that only shows when editing is available. */
export function EditButton({ kind, id, label = "Edit" }: { kind: Kind; id?: string; label?: string }) {
  const { editable, edit } = useEditor();
  const draft = useDraft();
  if (!editable) return null;
  const pending = id ? draft.find(kind, id) : undefined;
  const target = id ? targetFor(draft, kind, id) : { kind };
  return (
    <span className="edit-wrap">
      {pending && <span className={pending.value === null ? "pill pill-bad" : pending.id ? "pill pill-warn" : "pill pill-good"}>{pending.value === null ? "deleting" : pending.id ? "edited" : "new"}</span>}
      <button
        className="btn btn-small"
        onClick={(e) => {
          e.stopPropagation();
          edit(target);
        }}
      >
        {label}
      </button>
    </span>
  );
}
