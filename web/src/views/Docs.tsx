import type { ReactNode } from "react";
import type { ViewProps } from "../App";
import { Code, Section } from "../ui";

/** How fausto itself works: what it talks to, how its code is layered, and
 * what each workflow step reads and writes. Static; it describes the code,
 * not the spec, so it changes with fausto, not with system/. */

const pages = [
  { id: "overview", label: "Overview" },
  { id: "code", label: "Code" },
  { id: "workflow", label: "Workflow" },
  { id: "commands", label: "Commands" },
] as const;

type PageId = (typeof pages)[number]["id"];

export function DocsView({ item }: ViewProps) {
  const page: PageId = pages.some((p) => p.id === item) ? (item as PageId) : "overview";
  return (
    <Section
      title="How fausto works"
      aside={
        <nav className="segmented" aria-label="Documentation pages">
          {pages.map((p) => (
            <a key={p.id} href={`#/docs/${p.id}`} className={p.id === page ? "active" : ""} aria-current={p.id === page ? "page" : undefined}>
              {p.label}
            </a>
          ))}
        </nav>
      }
    >
      <div className="docs">
        {page === "overview" && <Overview />}
        {page === "code" && <CodeLayers />}
        {page === "workflow" && <Workflow />}
        {page === "commands" && <Commands />}
      </div>
    </Section>
  );
}

function Figure({ caption, children }: { caption: ReactNode; children: ReactNode }) {
  return (
    <figure className="doc-figure">
      <div className="doc-canvas">{children}</div>
      <figcaption>{caption}</figcaption>
    </figure>
  );
}

/** Arrowhead markers; ids are prefixed per figure so several can share a page. */
function Markers({ p }: { p: string }) {
  return (
    <defs>
      <marker id={`${p}-a`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
        <path className="tip" d="M0,0 L10,5 L0,10 z" />
      </marker>
      <marker id={`${p}-h`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
        <path className="tip hot" d="M0,0 L10,5 L0,10 z" />
      </marker>
      <marker id={`${p}-i`} viewBox="0 0 12 12" refX="11" refY="6" markerWidth="10" markerHeight="10" orient="auto-start-reverse">
        <path className="ring" d="M1,1 L11,6 L1,11 z" />
      </marker>
    </defs>
  );
}

/** A labeled box: a monospace name and a one-line note under it. */
function Box({ x, y, w, h = 52, name, note, kind, center }: {
  x: number; y: number; w: number; h?: number; name: string; note: string; kind?: "ext" | "hot" | "iface"; center?: boolean;
}) {
  const tx = center ? x + w / 2 : x + 16;
  const anchor = center ? "middle" : "start";
  const top = y + h / 2 - 3;
  return (
    <g>
      <rect className={`box${kind ? " " + kind : ""}`} x={x} y={y} width={w} height={h} rx={6} />
      <text className="t" x={tx} y={top} textAnchor={anchor}>{name}</text>
      <text className="s" x={tx} y={top + 18} textAnchor={anchor}>{note}</text>
    </g>
  );
}

function Overview() {
  const a = "url(#ov-a)";
  return (
    <>
      <p className="doc-prose">
        fausto keeps a project's <Code>system/</Code> and its code in step, runs the proposal workflow, and hands work to agents. It
        is a tool of its own, one Go binary used three ways: the orchestrator in a browser, agents from the command line, and CI for
        the drift checks. It works on each project only through its git repository, a local clone or one on GitHub, and never
        commits to the base branch: an accepted proposal becomes a branch (a pull request on GitHub) that people merge. Projects,
        proposals and tasks live in fausto's own MongoDB, deployed with it; Jira mirrors the tasks.
      </p>
      <Figure
        caption={
          <>
            <span className="doc-key"><span className="doc-swatch" />outside fausto</span>
            <span className="doc-key"><span className="doc-swatch hot" />source of truth for workflow state</span>
            Jira is a mirror: fausto pushes to it and only reads it back for display. Changes made in Jira never flow into fausto.
          </>
        }
      >
        <svg viewBox="0 0 1040 440" role="img" aria-label="fausto in the middle. The orchestrator uses its web UI over HTTP, agents run fausto task commands, and CI runs fausto check. fausto reads each project's git repository and puts every accepted proposal on a branch or pull request of its own, reads and writes projects, proposals and tasks in its MongoDB, and creates, attaches to and moves Jira issues while reading their status and assignee.">
          <Markers p="ov" />
          <Box x={20} y={58} w={200} h={64} name="Orchestrator" note="browser · localhost:7070" />
          <Box x={20} y={198} w={200} h={64} name="Agents" note="Claude Code, own worktree" />
          <Box x={20} y={338} w={200} h={64} name="CI" note="GitHub Actions" />

          <rect className="box" x={400} y={40} width={240} height={380} rx={8} />
          <text className="t big" x={520} y={196} textAnchor="middle">fausto</text>
          <text className="s" x={520} y={218} textAnchor="middle">one Go binary · cmd/fausto</text>
          <text className="l mono" x={520} y={250} textAnchor="middle">serve · check · agents</text>
          <text className="l mono" x={520} y={270} textAnchor="middle">task · project · jira sync</text>

          <Box x={820} y={58} w={200} h={64} name="Project repositories" note="local clone or GitHub" kind="ext" />
          <Box x={820} y={198} w={200} h={64} name="MongoDB" note="fausto's own · deployed with it" kind="hot" />
          <Box x={820} y={338} w={200} h={64} name="Jira Cloud" note="an issue per task, .md attached" kind="ext" />

          <line className="wire" x1={220} y1={90} x2={398} y2={90} markerEnd={a} />
          <text className="l" x={310} y={80} textAnchor="middle">reviews, proposes, accepts</text>
          <text className="s" x={310} y={108} textAnchor="middle">HTTP /api</text>
          <line className="wire" x1={220} y1={230} x2={398} y2={230} markerEnd={a} />
          <text className="l" x={310} y={220} textAnchor="middle">task pull · start · done</text>
          <text className="s" x={310} y={248} textAnchor="middle">tasks land in .fausto/tasks/</text>
          <line className="wire" x1={220} y1={370} x2={398} y2={370} markerEnd={a} />
          <text className="l" x={310} y={360} textAnchor="middle">fausto check</text>
          <text className="s" x={310} y={388} textAnchor="middle">needs no database</text>

          <line className="wire hot" x1={642} y1={90} x2={818} y2={90} markerEnd="url(#ov-h)" />
          <text className="l" x={730} y={80} textAnchor="middle">reads spec and code</text>
          <text className="l hot" x={730} y={108} textAnchor="middle">a branch or PR per accept</text>
          <line className="wire" x1={642} y1={230} x2={818} y2={230} markerStart={a} markerEnd={a} />
          <text className="l" x={730} y={220} textAnchor="middle">reads and writes</text>
          <text className="s" x={730} y={248} textAnchor="middle">projects · proposals · tasks</text>
          <line className="wire" x1={642} y1={362} x2={818} y2={362} markerEnd={a} />
          <text className="l" x={730} y={352} textAnchor="middle">create · attach .md · move</text>
          <line className="wire dash" x1={818} y1={380} x2={642} y2={380} markerEnd={a} />
          <text className="s" x={730} y={398} textAnchor="middle">reads status, assignee</text>
        </svg>
      </Figure>
    </>
  );
}

function CodeLayers() {
  const a = "url(#cl-a)";
  return (
    <>
      <p className="doc-prose">
        The workflow code depends on the <Code>store.Store</Code> interface, never on MongoDB, and on the <Code>source.Source</Code>{" "}
        interface, never on where a repository lives. Only <Code>cmd/fausto</Code> opens <Code>mongostore</Code> from{" "}
        <Code>FAUSTO_MONGO_URI</Code> and each project's source; tests use <Code>memstore</Code> and local repositories, and{" "}
        <Code>storetest</Code> holds both stores to one contract. The spec side (<Code>spec</Code>, <Code>edit</Code>,{" "}
        <Code>check</Code>, <Code>agentdef</Code>) reads files and never sees the database.
      </p>
      <Figure
        caption={
          <>
            Hollow triangles mean "implements". The dashed box is the boundary the rest of fausto codes against: swapping MongoDB for
            another database means one new package beside <Code>mongostore</Code> that passes <Code>storetest</Code>.
          </>
        }
      >
        <svg viewBox="0 0 1060 480" role="img" aria-label="Package layers. cmd/fausto drives server, proposal and jira. server calls proposal and jira and lists tasks from store.Store. proposal stores proposals and tasks through store.Store and puts each accepted proposal on a branch or pull request through source. jira records issue keys through store.Store and talks to Jira Cloud. mongostore and memstore both implement store.Store; mongostore talks to MongoDB.">
          <Markers p="cl" />
          <text className="head" x={20} y={60}>entry</text>
          <text className="head" x={20} y={180}>workflow</text>
          <text className="head" x={20} y={320}>storage</text>
          <text className="head" x={20} y={440}>backends</text>
          <line className="rule" x1={20} y1={110} x2={1040} y2={110} />
          <line className="rule" x1={20} y1={240} x2={1040} y2={240} />
          <line className="rule" x1={20} y1={380} x2={1040} y2={380} />

          <Box x={450} y={30} w={160} name="cmd/fausto" note="CLI · opens the store" center />
          <Box x={220} y={150} w={160} name="proposal" note="review · accept · sync" center />
          <Box x={450} y={150} w={160} name="server" note="web UI · /api" center />
          <Box x={680} y={150} w={160} name="jira" note="issue mirror" center />
          <Box x={900} y={150} w={140} name="Jira Cloud" note="REST v3" kind="ext" center />
          <Box x={220} y={290} w={160} name="source" note="local clone · GitHub" kind="ext" center />
          <Box x={450} y={290} w={160} name="store.Store" note="interface" kind="iface" center />
          <Box x={680} y={290} w={160} name="mongostore" note="driver v2" center />
          <Box x={450} y={410} w={160} name="memstore" note="in memory · tests" center />
          <Box x={680} y={410} w={160} name="MongoDB" note="compose.yaml" kind="hot" center />

          <line className="wire" x1={485} y1={82} x2={340} y2={148} markerEnd={a} />
          <text className="l" x={402} y={106} textAnchor="end">project</text>
          <line className="wire" x1={530} y1={82} x2={530} y2={148} markerEnd={a} />
          <text className="l" x={538} y={122}>serve</text>
          <line className="wire" x1={575} y1={82} x2={720} y2={148} markerEnd={a} />
          <text className="l" x={658} y={106}>jira sync · task</text>

          <line className="wire" x1={450} y1={176} x2={382} y2={176} markerEnd={a} />
          <text className="l" x={416} y={168} textAnchor="middle">calls</text>
          <line className="wire" x1={610} y1={176} x2={678} y2={176} markerEnd={a} />
          <text className="l" x={644} y={168} textAnchor="middle">syncs</text>
          <line className="wire" x1={840} y1={176} x2={898} y2={176} markerEnd={a} />

          <line className="wire hot" x1={300} y1={202} x2={300} y2={288} markerEnd="url(#cl-h)" />
          <text className="l hot" x={292} y={250} textAnchor="end">branch or PR per accept</text>
          <line className="wire" x1={360} y1={202} x2={470} y2={288} markerEnd={a} />
          <text className="l" x={420} y={234}>proposals, tasks</text>
          <line className="wire" x1={530} y1={202} x2={530} y2={288} markerEnd={a} />
          <text className="l" x={538} y={266}>lists tasks</text>
          <line className="wire" x1={700} y1={202} x2={590} y2={288} markerEnd={a} />
          <text className="l" x={664} y={266}>issue keys</text>

          <line className="wire dash" x1={680} y1={316} x2={612} y2={316} markerEnd="url(#cl-i)" />
          <text className="s" x={646} y={304} textAnchor="middle">implements</text>
          <line className="wire dash" x1={530} y1={410} x2={530} y2={344} markerEnd="url(#cl-i)" />
          <text className="s" x={538} y={374}>implements</text>
          <line className="wire" x1={760} y1={342} x2={760} y2={408} markerEnd={a} />
          <text className="s" x={768} y={374}>FAUSTO_MONGO_URI</text>
        </svg>
      </Figure>
    </>
  );
}

const steps = [
  { x: 280, head: "1 · propose", who: "orchestrator, web UI" },
  { x: 410, head: "2 · review", who: "orchestrator, web UI" },
  { x: 540, head: "3 · accept", who: "orchestrator, web UI" },
  { x: 670, head: "4 · merge", who: "people, git or GitHub" },
  { x: 810, head: "5 · pull", who: "agent, CLI" },
  { x: 950, head: "6 · start, done", who: "agent, CLI" },
];

const lanes = [
  { y: 110, name: "git", note: "the project's repo" },
  { y: 180, name: "MongoDB", note: "proposals, tasks" },
  { y: 250, name: "Jira", note: "mirror" },
  { y: 320, name: ".fausto/tasks/", note: "agent worktree, ignored" },
];

/** Each cell: the step (by x), the lane (by y), whether it writes, and what. */
const cells: { x: number; y: number; writes: boolean; text: string; hot?: boolean }[] = [
  { x: 280, y: 110, writes: false, text: "reads base" },
  { x: 280, y: 180, writes: true, text: "proposal + edits" },
  { x: 410, y: 110, writes: false, text: "applies to base" },
  { x: 410, y: 180, writes: false, text: "reads the edits" },
  { x: 540, y: 110, writes: true, text: "branch (or PR)", hot: true },
  { x: 540, y: 180, writes: true, text: "accepted" },
  { x: 670, y: 110, writes: true, text: "into the base", hot: true },
  { x: 670, y: 180, writes: true, text: "applied · tasks" },
  { x: 670, y: 250, writes: true, text: "issue + <id>.md" },
  { x: 810, y: 180, writes: false, text: "open tasks" },
  { x: 810, y: 320, writes: true, text: "writes <id>.md" },
  { x: 950, y: 180, writes: true, text: "status + who" },
  { x: 950, y: 250, writes: true, text: "moves issue" },
];

function Workflow() {
  return (
    <>
      <p className="doc-prose">
        What each step reads and writes. fausto never commits to the base branch: accepting puts the spec change on a branch of its
        own (a pull request, on GitHub), and people merge it. fausto notices the merge, marks the proposal applied and creates its
        tasks; until then agents get nothing, so they always work against a merged spec.
      </p>
      <Figure
        caption={
          <>
            <span className="doc-key">
              <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><circle className="dot" cx="6" cy="6" r="4.5" /></svg>writes
            </span>
            <span className="doc-key">
              <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><circle className="ring" cx="6" cy="6" r="4.5" /></svg>reads
            </span>
            A proposal is reviewed against the base branch as it is at review time. If an item it changes moved on the base since the
            proposal was made, review blocks the accept and it has to be proposed again.
          </>
        }
      >
        <svg viewBox="0 0 1060 380" role="img" aria-label="Six steps across four stores. Propose writes the proposal with its edits to MongoDB. Review applies the edits to the current base. Accept puts one commit touching system only on a branch of its own, or a pull request, and marks the proposal accepted. People merge it into the base; fausto then marks the proposal applied, creates its tasks in MongoDB and their Jira issues with the .md attached. Pull writes .md files into .fausto/tasks. Start and done write the status and history to MongoDB and move the Jira issue.">
          <rect className="band" x={475} y={66} width={260} height={300} rx={6} />
          {steps.map((s) => (
            <g key={s.x}>
              <text className="head" x={s.x} y={34} textAnchor="middle">{s.head}</text>
              <text className="s" x={s.x} y={52} textAnchor="middle">{s.who}</text>
            </g>
          ))}
          {lanes.map((l) => (
            <g key={l.y}>
              <text className="lane" x={20} y={l.y + 4}>{l.name}</text>
              <text className="s" x={20} y={l.y + 22}>{l.note}</text>
              <line className="rule" x1={200} y1={l.y} x2={1040} y2={l.y} />
            </g>
          ))}
          {cells.map((c) => (
            <g key={`${c.x}-${c.y}`}>
              <circle className={c.writes ? (c.hot ? "dot hot" : "dot") : "ring"} cx={c.x} cy={c.y} r={c.hot ? 7 : 6} />
              <text className={c.hot ? "l hot" : "l"} x={c.x} y={c.y + 24} textAnchor="middle">{c.text}</text>
            </g>
          ))}
        </svg>
      </Figure>
    </>
  );
}

const commands: [string, string][] = [
  ["fausto check", "Drift checks: the code against system/. No database needed; CI runs it."],
  ["fausto agents", "Regenerate .claude/agents/*.md and .github/CODEOWNERS from the agent charters."],
  ["fausto serve", "This UI, for every project (or only -root). Editing needs the database and a git identity."],
  ["fausto project add <id> -path <clone> | -github owner/repo", "Register a project; list and rm show and remove them."],
  ["fausto task pull <agent>", "Write the agent's open tasks to .fausto/tasks/ and remove closed ones."],
  ["fausto task start | done | drop <id>", "Change a task's status, record who did it, and move its Jira issue."],
  ["fausto task list [-agent a] [-status s]", "List tasks from the database."],
  ["fausto jira sync [-dry-run]", "Create missing issues, attach each task's .md, and move issues to the task's status."],
];

const settings: [string, string][] = [
  ["FAUSTO_MONGO_URI", "fausto's database; compose.yaml runs it with fausto. Without it, fausto is read-only and the task commands don't run."],
  ["FAUSTO_MONGO_DB", "Database name; defaults to fausto."],
  ["FAUSTO_PROJECT", "The project a command works on; defaults to the clone it runs in, or the only one."],
  ["FAUSTO_GITHUB_TOKEN", "For projects on GitHub: pushes proposal branches and opens pull requests (contents and pull requests, read and write)."],
  ["FAUSTO_DATA", "Where fausto keeps its clones of GitHub projects; defaults to the user cache directory."],
  ["FAUSTO_AUTHOR", "\"Name <email>\" for proposal commits and task history; defaults to git's identity."],
  ["FAUSTO_POLL", "How often to check accepted proposals for a merge; defaults to 1m."],
  ["FAUSTO_JIRA_URL, _EMAIL, _TOKEN, _PROJECT", "Turn on the Jira mirror. _STATUSES and _ISSUE_TYPE are optional."],
];

function Commands() {
  return (
    <>
      <p className="doc-prose">
        Install fausto with <Code>go install github.com/JuanVizcaya/fausto/cmd/fausto@latest</Code> and run it in a project's clone,
        or through the <Code>ghcr.io/juanvizcaya/fausto</Code> image. Settings come from the environment; with docker compose, put
        them in <Code>.env</Code> (<Code>.env.example</Code> lists them).
      </p>
      <div className="doc-tables">
        <table className="doc-table">
          <caption>Commands</caption>
          <tbody>
            {commands.map(([cmd, what]) => (
              <tr key={cmd}>
                <th scope="row"><code className="code">{cmd}</code></th>
                <td>{what}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <table className="doc-table">
          <caption>Settings</caption>
          <tbody>
            {settings.map(([name, what]) => (
              <tr key={name}>
                <th scope="row"><code className="code">{name}</code></th>
                <td>{what}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
