import type { ReactNode } from "react";

export function Pill({ tone = "neutral", children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`pill pill-${tone}`}>{children}</span>;
}

export type Tone = "neutral" | "good" | "bad" | "warn" | "info" | "muted";

export const statusTone: Record<string, Tone> = {
  active: "good",
  planned: "info",
  deprecated: "muted",
  pass: "good",
  fail: "bad",
  skipped: "muted",
  adopt: "good",
  trial: "info",
  assess: "warn",
  hold: "bad",
  accepted: "good",
  proposed: "info",
  superseded: "muted",
  rejected: "bad",
  manual: "muted",
  open: "info",
  "in-progress": "warn",
  blocked: "bad",
  mitigating: "warn",
  closed: "muted",
  done: "good",
  dropped: "muted",
};

export function StatusPill({ status }: { status: string }) {
  return <Pill tone={statusTone[status] ?? "neutral"}>{status}</Pill>;
}

export function Code({ children }: { children: ReactNode }) {
  return <code className="code">{children}</code>;
}

export function Section({ title, aside, children }: { title: string; aside?: ReactNode; children: ReactNode }) {
  return (
    <section className="section">
      <header className="section-head">
        <h2>{title}</h2>
        {aside}
      </header>
      {children}
    </section>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="empty">{children}</p>;
}

/** Renders the small Markdown subset used in ADRs: headings, paragraphs,
 * bullet lists, fenced code, and inline code. */
export function Markdown({ source, inline: asInline }: { source: string; inline?: boolean }) {
  if (asInline) return <>{inline(source)}</>;
  const blocks: ReactNode[] = [];
  const lines = source.split("\n");
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.startsWith("```")) {
      const code: string[] = [];
      for (i++; i < lines.length && !lines[i].startsWith("```"); i++) code.push(lines[i]);
      i++;
      blocks.push(<pre key={blocks.length} className="md-pre">{code.join("\n")}</pre>);
    } else if (/^#{1,6} /.test(line)) {
      blocks.push(<h4 key={blocks.length} className="md-h">{inline(line.replace(/^#+ /, ""))}</h4>);
      i++;
    } else if (/^\s*[-*] /.test(line)) {
      const items: string[] = [];
      for (; i < lines.length && /^\s*[-*] /.test(lines[i]); i++) items.push(lines[i].replace(/^\s*[-*] /, ""));
      blocks.push(<ul key={blocks.length}>{items.map((it, k) => <li key={k}>{inline(it)}</li>)}</ul>);
    } else if (line.trim() === "") {
      i++;
    } else {
      const para: string[] = [];
      for (; i < lines.length && lines[i].trim() !== "" && !/^(#{1,6} |```|\s*[-*] )/.test(lines[i]); i++) para.push(lines[i]);
      blocks.push(<p key={blocks.length}>{inline(para.join(" "))}</p>);
    }
  }
  return <div className="md">{blocks}</div>;
}

function inline(text: string): ReactNode[] {
  return text.split(/(`[^`]+`|\*\*[^*]+\*\*)/g).map((part, k) => {
    if (part.startsWith("`") && part.endsWith("`") && part.length > 1) return <Code key={k}>{part.slice(1, -1)}</Code>;
    if (part.startsWith("**") && part.endsWith("**") && part.length > 4) return <strong key={k}>{part.slice(2, -2)}</strong>;
    return part;
  });
}
