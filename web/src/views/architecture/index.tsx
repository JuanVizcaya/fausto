import { useState } from "react";
import type { ViewProps } from "../../App";
import { GithubBranchPicker } from "../../GithubBranchPicker";
import { CodeView } from "../code/CodeView";
import { SystemView } from "./Diagram";
import { Glossary } from "./Glossary";
import { TechRadar } from "./TechRadar";

// Sub-views other than the diagram are routed as ~<id>; component ids can't
// start with "~".
const subviews = [
  { id: "diagram", label: "Diagram", href: "#/architecture" },
  { id: "technology", label: "Technology", href: "#/architecture/~technology" },
  { id: "glossary", label: "Glossary", href: "#/architecture/~glossary" },
] as const;

export function ArchitectureView(props: ViewProps) {
  // The GitHub branch "View on GitHub" links use; session-only (not
  // persisted), defaults to the branch fausto proposals against.
  const [branch, setBranch] = useState(() => props.info?.base ?? "main");
  const repo = props.info?.repo;
  const [id, sub, ...rest] = props.item?.split("/") ?? [];
  const subview = id === "~technology" ? "technology" : id === "~glossary" ? "glossary" : "diagram";
  const bar = (
    <div className="arch-bar">
      <nav className="segmented" aria-label="Architecture views">
        {subviews.map((v) => (
          <a key={v.id} href={v.href} className={v.id === subview ? "active" : ""} aria-current={v.id === subview ? "page" : undefined}>
            {v.label}
          </a>
        ))}
      </nav>
      {repo && subview === "diagram" && (
        <div className="github-branch">
          <span className="muted small">GitHub</span>
          <GithubBranchPicker repo={repo} value={branch} onChange={setBranch} />
        </div>
      )}
    </div>
  );
  if (subview === "technology") {
    return (
      <>
        {bar}
        <TechRadar spec={props.spec} selected={sub} />
      </>
    );
  }
  if (subview === "glossary") {
    return (
      <>
        {bar}
        <Glossary spec={props.spec} />
      </>
    );
  }
  // Routes below a component drill into its code:
  // <component>/code[/<package dir>[/~/<symbol>]].
  const c = props.spec.system.components.find((x) => x.id === id);
  if (c && sub === "code") {
    const tilde = rest.indexOf("~");
    const dir = (tilde < 0 ? rest : rest.slice(0, tilde)).join("/") || undefined;
    const symbol = tilde < 0 ? undefined : rest.slice(tilde + 1).join("/") || undefined;
    return (
      <>
        {bar}
        <CodeView c={c} spec={props.spec} dir={dir} symbol={symbol} nav={(item) => props.go("architecture", item)} repo={repo} branch={branch} />
      </>
    );
  }
  return (
    <>
      {bar}
      <SystemView {...props} item={id || undefined} repo={repo} branch={branch} />
    </>
  );
}
