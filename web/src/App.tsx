import { useCallback, useEffect, useState } from "react";
import { fetchInfo, fetchProposals, fetchReport, fetchSchemas, fetchSpec } from "./api";
import { DraftProvider } from "./draft";
import { DraftBar } from "./DraftBar";
import { EditorProvider } from "./editor";
import type { Info, Proposal, Report, Schema, Spec } from "./types";
import { ArchitectureView } from "./views/architecture";
import { RoadmapView } from "./views/Roadmap";
import { QualityView } from "./views/Quality";
import { RisksView } from "./views/Risks";
import { EventsView } from "./views/Events";
import { DecisionsView } from "./views/Decisions";
import { AgentsView } from "./views/Agents";
import { HealthView } from "./views/Health";
import { ProposalsView } from "./views/Proposals";
import { TasksView } from "./views/Tasks";
import { DocsView } from "./views/Docs";

export interface ViewProps {
  spec: Spec;
  report: Report | null;
  proposals: Proposal[];
  /** Navigate to another view, optionally selecting an item in it. */
  go: (view: ViewId, item?: string) => void;
  /** Reload the spec, checks, and proposals. */
  reload: () => Promise<void>;
  item?: string;
  info: Info | null;
}

const views = [
  { id: "architecture", label: "Architecture", View: ArchitectureView },
  { id: "roadmap", label: "Roadmap", View: RoadmapView },
  { id: "quality", label: "Quality", View: QualityView },
  { id: "risks", label: "Risks", View: RisksView },
  { id: "events", label: "Events", View: EventsView },
  { id: "decisions", label: "Decisions", View: DecisionsView },
  { id: "agents", label: "Agents", View: AgentsView },
  { id: "proposals", label: "Proposals", View: ProposalsView },
  { id: "tasks", label: "Tasks", View: TasksView },
  { id: "health", label: "Health", View: HealthView },
  { id: "docs", label: "Docs", View: DocsView },
] as const;

export type ViewId = (typeof views)[number]["id"];

function parseHash(): { view: ViewId; item?: string } {
  // The item may have several segments, as the architecture's drill-down
  // paths do: component/code/package/dir/~/Symbol.
  const [hashView, ...rest] = window.location.hash.replace(/^#\/?/, "").split("/").map(decodeURIComponent);
  // Old links to views since folded into others land where their content went.
  const folded: Record<string, [ViewId, string]> = {
    tech: ["architecture", rest.length ? `~technology/${rest.join("/")}` : "~technology"],
    layers: ["architecture", rest.length ? `layer:${rest[0]}` : ""],
    domains: ["architecture", ""],
    principles: ["health", ""],
  };
  const [view, item] = Object.hasOwn(folded, hashView) ? folded[hashView] : [hashView, rest.join("/")];
  const known = views.some((v) => v.id === view);
  return { view: known ? (view as ViewId) : "architecture", item: known ? item || undefined : undefined };
}

export default function App() {
  const [route, setRoute] = useState(parseHash);
  const [spec, setSpec] = useState<Spec | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [info, setInfo] = useState<Info | null>(null);
  const [schemas, setSchemas] = useState<Record<string, Schema> | null>(null);
  const [proposals, setProposals] = useState<Proposal[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [hideReadonly, setHideReadonly] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [s, r, i, sc] = await Promise.all([fetchSpec(), fetchReport(), fetchInfo(), fetchSchemas()]);
      setSpec(s);
      setReport(r);
      setInfo(i);
      setSchemas(sc);
      setProposals(i.editable ? await fetchProposals() : []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const onHash = () => setRoute(parseHash());
    // Agents change files while fausto is open; recheck on return.
    const onFocus = () => load();
    window.addEventListener("hashchange", onHash);
    window.addEventListener("focus", onFocus);
    return () => {
      window.removeEventListener("hashchange", onHash);
      window.removeEventListener("focus", onFocus);
    };
  }, [load]);

  const go = useCallback((view: ViewId, item?: string) => {
    window.location.hash = `/${view}${item ? "/" + item.split("/").map(encodeURIComponent).join("/") : ""}`;
  }, []);

  const failed = report?.results.filter((r) => r.status === "fail").length ?? 0;
  const openProposals = proposals.filter((p) => p.status === "open").length;
  const openTasks = spec?.tasks.filter((t) => t.status === "open" || t.status === "in-progress").length ?? 0;
  const current = views.find((v) => v.id === route.view)!;
  const badge = (id: ViewId) => {
    if (id === "health" && report) return <span className={failed ? "tab-badge bad" : "tab-badge good"}>{failed || "✓"}</span>;
    if (id === "proposals" && openProposals) return <span className="tab-badge info">{openProposals}</span>;
    if (id === "tasks" && openTasks) return <span className="tab-badge info">{openTasks}</span>;
    return null;
  };

  return (
    <DraftProvider>
      <EditorProvider spec={spec} schemas={schemas} editable={!!info?.editable}>
        <div className="app">
          <header className="topbar">
            <div className="brand">
              <picture className="brand-mark">
                <source srcSet="/fausto-logo-light.svg" media="(prefers-color-scheme: dark)" />
                <img src="/fausto-logo.svg" alt="" width={20} height={20} />
              </picture>
              <span className="brand-name">Fausto</span>
              <span className="brand-sub">
                {spec?.system.name ?? "fausto"}
                {info?.base && ` · ${info.base}`}
              </span>
            </div>
            <nav className="tabs" aria-label="Views">
              {views.filter((v) => v.id !== "docs").map((v) => (
                <a key={v.id} href={`#/${v.id}`} className={v.id === route.view ? "tab active" : "tab"}>
                  {v.label}
                  {badge(v.id)}
                </a>
              ))}
            </nav>
            {/* Outside the scrolling tab row, so it is always visible. */}
            <a href="#/docs" className={route.view === "docs" ? "tab active" : "tab"} title="How fausto works">
              Docs
            </a>
            <button className="btn" onClick={load} disabled={loading} title="Reload system/ and rerun checks">
              {loading ? "Checking…" : "Refresh"}
            </button>
          </header>
          <main className="main">
            {info && !info.editable && !hideReadonly && (
              <div className="banner warn readonly" role="status">
                <span>
                  <strong>Read-only.</strong> Editing is off: {info.readonlyReason ?? "fausto has no proposal store."}
                </span>
                <button className="btn btn-ghost btn-small" onClick={() => setHideReadonly(true)} aria-label="Dismiss">
                  ×
                </button>
              </div>
            )}
            {error && (
              <div className="banner bad" role="alert">
                <strong>Could not load the spec.</strong> {error}
              </div>
            )}
            {spec ? (
              <current.View spec={spec} report={report} proposals={proposals} go={go} reload={load} item={route.item} info={info} />
            ) : (
              !error && <p className="empty">Loading…</p>
            )}
          </main>
          <DraftBar
            base={info?.base}
            milestones={spec?.roadmap.milestones ?? []}
            onCreated={async (id) => {
              await load();
              go("proposals", id);
            }}
          />
        </div>
      </EditorProvider>
    </DraftProvider>
  );
}
