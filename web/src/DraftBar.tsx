import { useState } from "react";
import { ApiError, createProposal } from "./api";
import { useDraft } from "./draft";
import { useEditor } from "./editor";
import type { Finding, Milestone } from "./types";

export function DraftBar({ base, milestones, onCreated }: { base?: string; milestones: Milestone[]; onCreated: (id: string) => void }) {
  const draft = useDraft();
  const { edit } = useEditor();
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [milestone, setMilestone] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; findings: Finding[] } | null>(null);
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  if (draft.changes.length === 0) return null;
  const n = draft.changes.length;

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const changes = draft.changes.map(({ kind, id, value, body, order }) => ({ kind, id, value, body, order }));
      const created = await createProposal(title, description, milestone, changes);
      draft.clear();
      setTitle("");
      setDescription("");
      setMilestone("");
      setOpen(false);
      onCreated(created.id);
    } catch (e) {
      setError(e instanceof ApiError ? { message: e.message, findings: e.findings } : { message: String(e), findings: [] });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className={open ? "draftbar open" : "draftbar"}>
      <div className="draftbar-row">
        <strong>
          {n} pending {n === 1 ? "change" : "changes"}
        </strong>
        <span className="muted">Draft is saved in this browser until you propose it.</span>
        <span className="spacer" />
        <button className="btn btn-primary" onClick={() => setOpen(!open)} aria-expanded={open}>
          {open ? "Hide" : "Review and propose"}
        </button>
      </div>
      {open && (
        <div className="draftbar-panel">
          <ul className="draft-list">
            {draft.changes.map((c) => (
              <li key={c.key}>
                <span className={c.value === null ? "pill pill-bad" : c.id || c.order ? "pill pill-warn" : "pill pill-good"}>
                  {c.value === null ? "delete" : c.order ? "reorder" : c.id ? "edit" : "add"}
                </span>
                <button className="link" onClick={() => edit({ kind: c.kind, id: c.id, draftKey: c.key })} disabled={c.value === null || !!c.order}>
                  {c.label}
                </button>
                <button className="btn btn-small btn-ghost" onClick={() => draft.remove(c.key)} aria-label={`Undo ${c.label}`}>
                  Undo
                </button>
              </li>
            ))}
          </ul>
          <div className="field">
            <label className="field-label" htmlFor="proposal-title">
              Proposal title<span className="req">*</span>
            </label>
            <input id="proposal-title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="e.g. Add the billing service" />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="proposal-description">
              Why
            </label>
            <textarea
              id="proposal-description"
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Context for the agents who will implement it."
            />
          </div>
          <div className="field">
            <label className="field-label" htmlFor="proposal-milestone">
              Counts toward milestone
            </label>
            <select id="proposal-milestone" value={milestone} onChange={(e) => setMilestone(e.target.value)}>
              <option value="">None</option>
              {milestones
                .filter((m) => m.status === "planned" || m.status === "in-progress")
                .map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.title}
                  </option>
                ))}
            </select>
            <p className="field-help">Tasks created when this proposal is accepted are linked to the milestone.</p>
          </div>
          {error && (
            <div className="banner bad" role="alert">
              {error.findings.length ? "The proposed spec is invalid:" : error.message}
              {!!error.findings.length && (
                <ul>
                  {error.findings.map((f, i) => (
                    <li key={i}>
                      {f.path && <code className="code">{f.path}</code>} {f.message}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
          <div className="draftbar-actions">
            {confirmDiscard ? (
              <>
                <span>Discard all {n} changes?</span>
                <button className="btn btn-danger" onClick={() => { draft.clear(); setConfirmDiscard(false); }}>
                  Discard
                </button>
                <button className="btn" onClick={() => setConfirmDiscard(false)}>
                  Keep
                </button>
              </>
            ) : (
              <button className="btn btn-ghost" onClick={() => setConfirmDiscard(true)}>
                Discard draft
              </button>
            )}
            <span className="spacer" />
            <span className="muted small">Creates branch proposal/… from {base ?? "the base branch"}; nothing is merged yet.</span>
            <button className="btn btn-primary" onClick={submit} disabled={busy || !title.trim()}>
              {busy ? "Creating…" : "Create proposal"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
