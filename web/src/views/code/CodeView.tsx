import { useEffect, useMemo, useState, type ReactNode } from "react";
import { fetchCode } from "../../api";
import type { Component, Spec } from "../../types";
import { Code, Empty, Pill, Section } from "../../ui";
import { githubBlobUrl, githubTreeUrl } from "../../github";
import { CallGraph } from "./CallGraph";
import { ComponentLevel } from "./ComponentLevel";
import { CodeIndex, codeItem, shortDir, type Sym } from "./model";
import { PackageLevel, shortSig } from "./PackageLevel";

/**
 * The drill-down below the architecture diagram: a component's packages
 * (level 2), a package's class diagram (level 3), and a function's calls or
 * a type's details (level 4).
 */
export function CodeView({
  c,
  spec,
  dir,
  symbol,
  nav,
  repo,
  branch,
}: {
  c: Component;
  spec: Spec;
  dir?: string;
  symbol?: string;
  nav: (item?: string) => void;
  repo?: string;
  branch: string;
}) {
  const [code, setCode] = useState<CodeIndex | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Refetch with the spec, which fausto reloads on Refresh and focus.
  useEffect(() => {
    let live = true;
    fetchCode(c.id)
      .then((m) => live && (setCode(new CodeIndex(m)), setError(null)))
      .catch((e: Error) => live && setError(e.message));
    return () => {
      live = false;
    };
  }, [c.id, spec]);

  const loaded = code?.model.component === c.id ? code : null;
  const pkg = dir ? loaded?.packages.get(dir) : undefined;
  const sym = pkg && symbol ? loaded?.symbols.get(`${pkg.dir}#${symbol}`) : undefined;

  const crumbs: { label: string; item?: string }[] = [
    { label: "System", item: c.id },
    { label: c.name, item: codeItem(c.id) },
  ];
  if (dir) crumbs.push({ label: shortDir(dir), item: codeItem(c.id, dir) });
  if (symbol && dir) {
    const [type, method] = symbol.split(".");
    if (method) crumbs.push({ label: type, item: codeItem(c.id, dir, type) });
    crumbs.push({ label: method ?? type });
  }
  crumbs[crumbs.length - 1].item = undefined;

  let title = `Code of ${c.name}`;
  let body: ReactNode;
  let aside: ReactNode;
  if (error) {
    body = <div className="banner bad">Could not analyze the code: {error}</div>;
  } else if (!loaded) {
    body = <p className="empty">Reading the code…</p>;
  } else if (!dir) {
    title = `Packages of ${c.name}`;
    body = loaded.model.packages.length ? (
      <ComponentLevel c={c} code={loaded} spec={spec} nav={nav} />
    ) : (
      <Empty>{c.status === "planned" ? "No code yet: this component is planned." : "This component has no Go code of its own."}</Empty>
    );
    aside = <ComponentPanel c={c} code={loaded} nav={nav} repo={repo} branch={branch} />;
  } else if (!pkg) {
    body = <Empty>No Go package at <Code>{dir}</Code> in {c.name}.</Empty>;
  } else if (symbol && !sym) {
    body = <PackageLevel component={c.id} pkg={pkg} code={loaded} nav={nav} />;
    aside = <Empty>No symbol {symbol} in this package any more.</Empty>;
  } else if (sym && sym.kind !== "type") {
    title = `Calls of ${loaded.label(sym.id)}`;
    body = <CallGraph id={sym.id} code={loaded} nav={nav} />;
    aside = <SymbolPanel sym={sym} code={loaded} nav={nav} repo={repo} branch={branch} />;
  } else {
    title = `Classes in ${shortDir(pkg.dir)}`;
    body = <PackageLevel component={c.id} pkg={pkg} code={loaded} selected={sym?.id} nav={nav} />;
    aside = sym ? (
      <SymbolPanel sym={sym} code={loaded} nav={nav} repo={repo} branch={branch} />
    ) : (
      <PackagePanel pkg={pkg} code={loaded} nav={nav} repo={repo} branch={branch} />
    );
  }

  return (
    <div className="split">
      <Section title={title} aside={<span className="muted small">Read-only: code belongs to {c.owner}</span>}>
        <nav className="crumbs" aria-label="Drill-down">
          {crumbs.map((cr, i) => (
            <span key={i}>
              {i > 0 && <span className="crumb-sep">›</span>}
              {cr.item !== undefined ? (
                <button className="link" onClick={() => nav(cr.item)}>
                  {cr.label}
                </button>
              ) : (
                <strong>{cr.label}</strong>
              )}
            </span>
          ))}
        </nav>
        {body}
      </Section>
      <aside className="detail">{aside ?? <Empty>Select something in the diagram.</Empty>}</aside>
    </div>
  );
}

function ComponentPanel({
  c,
  code,
  nav,
  repo,
  branch,
}: {
  c: Component;
  code: CodeIndex;
  nav: (item: string) => void;
  repo?: string;
  branch: string;
}) {
  const m = code.model;
  const byComponent = new Map<string, string[]>();
  for (const d of m.dependents) byComponent.set(d.component ?? "other", [...new Set([...(byComponent.get(d.component ?? "other") ?? []), d.from])]);
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>{c.name}</h3>
        <button className="btn btn-small" onClick={() => nav(c.id)}>
          ‹ System
        </button>
      </div>
      <p className="muted">
        {m.packages.length} Go packages. Read from the source on each refresh; types, interfaces, and calls are matched by name and shape, without
        compiling, so treat them as a guide.
      </p>
      <h4>Packages</h4>
      <ul className="plain">
        {m.packages.map((p) => (
          <li key={p.dir}>
            <button className="link" onClick={() => nav(codeItem(c.id, p.dir))}>
              {shortDir(p.dir)}
            </button>{" "}
            <Pill>{p.role}</Pill>
            {repo && (
              <>
                {" "}
                <a href={githubTreeUrl(repo, branch, p.dir)} target="_blank" rel="noreferrer" className="link small">
                  ↗
                </a>
              </>
            )}
            {p.doc && <div className="muted small">{p.doc.split("\n")[0]}</div>}
          </li>
        ))}
      </ul>
      {m.contracts.map((ct) => (
        <div key={ct.proto}>
          <h4>
            Contract <Code>{ct.proto}</Code>
          </h4>
          {ct.services.map((s) => (
            <div key={s.name}>
              <div>
                service <strong>{s.name}</strong>
              </div>
              <ul className="plain mono small">
                {s.rpcs.map((r) => (
                  <li key={r.name}>
                    {r.name}({r.clientStreams && "stream "}
                    {r.request}) → {r.serverStreams && "stream "}
                    {r.response}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      ))}
      {byComponent.size > 0 && (
        <>
          <h4>Used by</h4>
          <ul className="plain">
            {[...byComponent].map(([comp, dirs]) => (
              <li key={comp}>
                <button className="link" onClick={() => nav(codeItem(comp))}>
                  {comp}
                </button>{" "}
                <span className="muted small">{dirs.map(shortDir).join(", ")}</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function PackagePanel({
  pkg,
  code,
  nav,
  repo,
  branch,
}: {
  pkg: Pick<Sym["pkg"], "dir" | "name" | "doc" | "files" | "imports" | "external" | "types" | "funcs" | "role">;
  code: CodeIndex;
  nav: (item: string) => void;
  repo?: string;
  branch: string;
}) {
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3>
          package <span className="mono">{pkg.name}</span>
        </h3>
        <Pill>{pkg.role}</Pill>
      </div>
      {pkg.doc && <p>{pkg.doc}</p>}
      <p className="muted">
        {pkg.types.length} types, {pkg.funcs.length} functions in {pkg.files.length} files.
        {repo && (
          <>
            {" "}
            <a href={githubTreeUrl(repo, branch, pkg.dir)} target="_blank" rel="noreferrer" className="link small">
              View on GitHub ↗
            </a>
          </>
        )}
      </p>
      <h4>Files</h4>
      <ul className="plain mono small">
        {pkg.files.map((f) => (
          <li key={f}>
            {repo ? (
              <a href={githubBlobUrl(repo, branch, f)} target="_blank" rel="noreferrer">
                {f.split("/").pop()}
              </a>
            ) : (
              f.split("/").pop()
            )}
          </li>
        ))}
      </ul>
      {pkg.imports.length > 0 && (
        <>
          <h4>Imports in the repository</h4>
          <ul className="plain">
            {pkg.imports.map((i) => (
              <li key={i.dir}>
                {i.component ? (
                  <button className="link" onClick={() => nav(codeItem(i.component!, i.dir))}>
                    {shortDir(i.dir)}
                  </button>
                ) : (
                  <Code>{i.dir}</Code>
                )}{" "}
                {i.kind !== "same" && <Pill tone={i.kind === "other" ? "bad" : "neutral"}>{i.kind}</Pill>}
              </li>
            ))}
          </ul>
        </>
      )}
      {pkg.external.length > 0 && (
        <>
          <h4>Other imports</h4>
          <ul className="plain mono small">
            {pkg.external.map((e) => (
              <li key={e}>{e}</li>
            ))}
          </ul>
        </>
      )}
      <p className="muted small">{code.model.calls.length} calls resolved in this component.</p>
    </div>
  );
}

function SymbolLink({ id, code, nav, via, rpc }: { id: string; code: CodeIndex; nav: (item: string) => void; via?: string; rpc?: boolean }) {
  const item = code.itemFor(id);
  return (
    <li>
      {item ? (
        <button className="link mono" onClick={() => nav(item)}>
          {code.label(id)}
        </button>
      ) : (
        <span className="mono">{code.label(id)}</span>
      )}
      {via && <span className="muted small"> through {code.label(via)}</span>}
      {rpc && <span className="muted small"> over gRPC</span>}
    </li>
  );
}

function SymbolList({ title, ids, code, nav }: { title: string; ids: { id: string; via?: string; rpc?: boolean }[]; code: CodeIndex; nav: (item: string) => void }) {
  if (!ids.length) return null;
  return (
    <>
      <h4>{title}</h4>
      <ul className="plain">
        {ids.map((x) => (
          <SymbolLink key={x.id} id={x.id} via={x.via} rpc={x.rpc} code={code} nav={nav} />
        ))}
      </ul>
    </>
  );
}

function SymbolPanel({
  sym,
  code,
  nav,
  repo,
  branch,
}: {
  sym: Sym;
  code: CodeIndex;
  nav: (item: string) => void;
  repo?: string;
  branch: string;
}) {
  const pos = sym.method?.pos ?? sym.func?.pos ?? sym.type?.pos;
  const doc = sym.method?.doc ?? sym.func?.doc ?? (sym.kind === "type" ? sym.type?.doc : undefined);
  const source = sym.method?.source ?? sym.func?.source ?? (sym.kind === "type" ? sym.type?.source : undefined);
  const sig = sym.method?.sig ?? sym.func?.sig;
  const ids = (l: string[]) => l.map((id) => ({ id }));
  const relations = useMemo(() => {
    if (sym.kind !== "type") return { out: [], in: [] };
    const all = code.model.packages.flatMap((p) => p.relations);
    return { out: all.filter((r) => r.from === sym.id && r.kind !== "implements"), in: all.filter((r) => r.to === sym.id && r.kind !== "implements") };
  }, [sym, code]);
  const kindLabel =
    sym.kind === "type" ? sym.type!.kind : sym.kind === "method" ? (sym.type?.kind === "interface" ? "interface method" : "method") : "function";
  return (
    <div className="detail-body">
      <div className="detail-head">
        <h3 className="mono">{sym.name}</h3>
        <Pill>{kindLabel}</Pill>
      </div>
      {pos && (
        <p className="muted">
          <Code>
            {pos.file}:{pos.line}
          </Code>
          {repo && (
            <>
              {" "}
              <a href={githubBlobUrl(repo, branch, pos.file, pos.line)} target="_blank" rel="noreferrer" className="link small">
                View on GitHub ↗
              </a>
            </>
          )}
        </p>
      )}
      {sig && <pre className="source sig">{`${sym.method ? `func (${sym.method.pointer ? "*" : ""}${sym.type?.name}) ` : "func "}${sym.method?.name ?? sym.func?.name}${sig}`}</pre>}
      {doc && <p>{doc}</p>}
      {sym.kind === "type" && (
        <>
          <SymbolList title="Implements" ids={ids(code.interfaces(sym.id))} code={code} nav={nav} />
          <SymbolList title="Implemented by" ids={ids(code.implementers(sym.id))} code={code} nav={nav} />
          {relations.out.length > 0 && (
            <>
              <h4>Refers to</h4>
              <ul className="plain">
                {relations.out.map((r) => (
                  <li key={r.to + r.kind}>
                    <span className="muted small">{r.kind === "has" ? `field ${r.label}` : r.kind}</span>{" "}
                    <SymbolInline id={r.to} code={code} nav={nav} />
                  </li>
                ))}
              </ul>
            </>
          )}
          {relations.in.length > 0 && (
            <>
              <h4>Referred to by</h4>
              <ul className="plain">
                {relations.in.map((r) => (
                  <li key={r.from + r.kind}>
                    <SymbolInline id={r.from} code={code} nav={nav} /> <span className="muted small">{r.kind === "has" ? `field ${r.label}` : r.kind}</span>
                  </li>
                ))}
              </ul>
            </>
          )}
          <SymbolList title="Methods" ids={sym.type!.methods.map((m) => ({ id: m.id }))} code={code} nav={nav} />
        </>
      )}
      {sym.kind !== "type" && (
        <>
          <SymbolList title="Implemented by" ids={ids(code.implementations(sym.id))} code={code} nav={nav} />
          <SymbolList title="Called by" ids={code.callers(sym.id).map((c) => ({ ...c, rpc: code.isRPC(c.id, sym.id) }))} code={code} nav={nav} />
          <SymbolList title="Calls" ids={code.callees(sym.id).map((id) => ({ id, rpc: code.isRPC(sym.id, id) }))} code={code} nav={nav} />
          {sym.type && (
            <p className="small">
              On{" "}
              <button className="link mono" onClick={() => nav(code.itemFor(sym.type!.id)!)}>
                {sym.type.name}
              </button>
              <span className="muted"> · {shortSig(sig ?? "")}</span>
            </p>
          )}
        </>
      )}
      {source && (
        <>
          <h4>Source</h4>
          <pre className="source">{source}</pre>
        </>
      )}
    </div>
  );
}

function SymbolInline({ id, code, nav }: { id: string; code: CodeIndex; nav: (item: string) => void }) {
  const item = code.itemFor(id);
  return item ? (
    <button className="link mono" onClick={() => nav(item)}>
      {code.label(id)}
    </button>
  ) : (
    <span className="mono">{code.label(id)}</span>
  );
}
