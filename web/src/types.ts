// Mirrors the JSON from fausto serve: /api/spec and /api/check.

export interface Spec {
  system: System;
  tech: { technologies: Technology[] };
  principles: { principles: Principle[] };
  domains: { domains: Domain[]; glossary?: Term[] };
  roadmap: { milestones: Milestone[] };
  requirements: { requirements: Requirement[] };
  runtimeRules: string[];
  risks: { risks: Risk[] };
  events: { events: EventContract[] };
  agents: Agent[];
  decisions: Decision[];
  tasks: Task[];
  /** Where the agent charters are kept: in the repository's system/agents/, or in fausto. */
  charters: "repo" | "fausto" | "";
}

export interface System {
  name: string;
  description?: string;
  layers: Layer[];
  components: Component[];
}

export interface Layer {
  id: string;
  description?: string;
  may_depend_on: string[];
}

export type ComponentKind = "service" | "gateway" | "database" | "frontend" | "library";
export type Status = "planned" | "active" | "deprecated";

export interface Component {
  id: string;
  name: string;
  description?: string;
  kind: ComponentKind;
  layer: string;
  status: Status;
  owner: string;
  tech?: string[];
  paths?: string[];
  runtime?: { cmd?: string; compose?: string };
  contracts?: { proto: string; generated?: string }[];
  data?: { table: string; pii?: string[] }[];
  depends_on?: Dependency[];
}

export interface Dependency {
  target: string;
  protocol: "http" | "grpc" | "sql" | "library";
  description?: string;
}

export interface Technology {
  id: string;
  name: string;
  category: string;
  status: "adopt" | "trial" | "assess" | "hold";
  notes?: string;
}

export interface Milestone {
  id: string;
  title: string;
  goal: string;
  status: "planned" | "in-progress" | "done" | "dropped";
  target?: string;
  depends_on?: string[];
  components?: string[];
  decisions?: string[];
  exit_criteria?: { text: string; done?: boolean }[];
}

export interface Requirement {
  id: string;
  title: string;
  category: "availability" | "performance" | "security" | "privacy" | "operability" | "durability";
  statement: string;
  target?: string;
  applies_to?: { kinds?: string[]; layers?: string[]; components?: string[] };
  verified_by?: string;
}

/** An asynchronous event between services (named to avoid DOM Event). */
export interface EventContract {
  id: string;
  title: string;
  description: string;
  status: "planned" | "active" | "deprecated";
  producer: string;
  consumers?: string[];
  message: string;
  key?: string;
  delivery?: "at-least-once" | "at-most-once";
}

export type Level = "low" | "medium" | "high";

export interface Risk {
  id: string;
  title: string;
  description: string;
  category: "security" | "technical" | "operational" | "delivery" | "compliance";
  likelihood: Level;
  impact: Level;
  status: "open" | "mitigating" | "accepted" | "closed";
  owner: string;
  mitigation?: string;
  review_by?: string;
  components?: string[];
  milestones?: string[];
  requirements?: string[];
  decisions?: string[];
}

export interface Principle {
  id: string;
  title: string;
  rationale: string;
  checks?: string[];
}

export interface Domain {
  id: string;
  name: string;
  description?: string;
  status: Status;
  components?: string[];
}

export interface Term {
  term: string;
  definition: string;
}

export interface Agent {
  id: string;
  name: string;
  kind: "agent" | "human";
  scope: string;
  owns: string[];
  rules?: string[];
  file: string;
}

export interface Decision {
  id: string;
  title: string;
  status: "proposed" | "accepted" | "superseded" | "rejected";
  date: string;
  supersedes?: string;
  affects?: string[];
  body: string;
  file: string;
}

export interface Finding {
  message: string;
  component?: string;
  path?: string;
  rule?: string;
}

export interface CheckResult {
  check: string;
  description: string;
  principles: string[];
  status: "pass" | "fail" | "skipped";
  findings: Finding[];
}

export interface Report {
  ok: boolean;
  results: CheckResult[];
}

export interface Task {
  id: string;
  agent: string;
  proposal?: string;
  milestone?: string;
  status: "open" | "in-progress" | "done" | "dropped";
  created: string;
  title: string;
  components?: string[];
  /** Jira issue key, once synced. */
  jira?: string;
  body: string;
  /** Who changed the status, oldest first. */
  history?: { at: string; by: string; from: string; to: string }[];
  updated: string;
}

/** What a Jira sync did, one result per task. */
export interface JiraReport {
  dryRun?: boolean;
  results: { task: string; key?: string; url?: string; actions: string[]; error?: string }[];
  commit?: string;
}

/** A task's issue as it is in Jira right now. */
export interface JiraIssue {
  key: string;
  url: string;
  status: string;
  assignee?: string;
  avatar?: string;
  priority?: string;
  updated?: string;
  /** The fausto task id, from the issue's label. */
  task?: string;
  /** The Jira status the task's status maps to. */
  want?: string;
}

/** A best-effort sync after a change; the change itself succeeded. */
export interface JiraOutcome {
  report?: JiraReport;
  error?: string;
}

/** A project fausto serves, from /api/projects. */
export interface ProjectInfo {
  id: string;
  name: string;
  /** A local path, or "github.com/owner/repo". */
  source: string;
  /** Why the project couldn't be opened. */
  error?: string;
}

/** What syncing accepted proposals with their reviews did. */
export interface SyncReport {
  results: { proposal: string; status?: Proposal["status"]; tasks?: string[]; error?: string }[];
  jiraSync?: JiraOutcome;
}

export interface Info {
  editable: boolean;
  base?: string;
  /** Where accepted proposals go: a pull request on GitHub, or a branch of a local clone. */
  source?: "github" | "local";
  /** Why editing is off, when it is. */
  readonlyReason?: string;
  /** The repository's GitHub URL, e.g. "https://github.com/owner/repo". */
  repo?: string;
  /** Set when fausto mirrors tasks as Jira issues. */
  jira?: { url: string; project: string };
}

/** A JSON Schema, loosely typed: fausto only reads common keywords. */
export type Schema = {
  type?: string;
  enum?: string[];
  properties?: Record<string, Schema>;
  required?: string[];
  items?: Schema;
  $ref?: string;
  $defs?: Record<string, Schema>;
  description?: string;
  pattern?: string;
  minItems?: number;
  title?: string;
};

export type Kind =
  | "component"
  | "layer"
  | "technology"
  | "principle"
  | "domain"
  | "term"
  | "agent"
  | "decision"
  | "milestone"
  | "requirement"
  | "risk"
  | "event";

/** One pending edit. value null deletes the item. */
export interface Change {
  kind: Kind;
  id?: string;
  value: Record<string, unknown> | null;
  body?: string;
  /** Reorders the list of kind instead: these items, in this order, take
   * the places they held. Items not named keep their places. */
  order?: string[];
}

export interface Proposal {
  id: string;
  title: string;
  description: string;
  author: string;
  created: string;
  /** accepted: its change is on a branch (and in a pull request on
   * GitHub) waiting to be merged; applied: merged, and its tasks created;
   * withdrawn: its pull request was closed without merging. */
  status: "open" | "accepted" | "applied" | "rejected" | "withdrawn";
  milestone?: string;
  accepted?: string;
  applied?: string;
  /** When it was rejected or withdrawn. */
  closed?: string;
  /** The tasks applying it created. */
  tasks: string[];
  /** The base branch commit it was made against. */
  baseCommit?: string;
  /** The commit with its spec change, once accepted. */
  commit?: string;
  /** The branch holding commit, and its pull request on GitHub. */
  branch?: string;
  pr?: number;
  url?: string;
  /** The base branch commit that merged it, when known. */
  merged?: string;
  /** Imported from git without its edits. */
  legacy?: boolean;
  updated: string;
}

export interface ItemChange {
  kind: Kind;
  id: string;
  name?: string;
  action: "added" | "removed" | "modified";
  fields?: string[];
}

export interface TaskPlan {
  id: string;
  agent: string;
  components: string[];
  specChanges: string[];
  findings: string[];
}

export interface ProposalDetail extends Proposal {
  base: string;
  changes: ItemChange[];
  files: string[];
  diff: string;
  report: Report | null;
  plans: TaskPlan[];
  unassigned: string[];
  conflict?: string;
}

/** The code of one component, from /api/code/{component}. */
export interface CodeModel {
  component: string;
  packages: CodePackage[];
  neighbors: { dir: string; name: string; component?: string; kind: ImportKind }[];
  dependents: { from: string; component?: string; to: string }[];
  contracts: CodeContract[];
  stubs: CodeStub[];
  calls: CodeCall[];
  /** Names and components of the packages the calls' ends are in. */
  dirs: Record<string, { name: string; component?: string }>;
}

export type ImportKind = "same" | "contract" | "library" | "other";

export interface CodePackage {
  dir: string;
  name: string;
  doc?: string;
  role: "cmd" | "internal" | "generated";
  files: string[];
  imports: { dir: string; component?: string; kind: ImportKind }[];
  external: string[];
  database: boolean;
  types: CodeType[];
  funcs: CodeFunc[];
  relations: CodeRelation[];
}

export interface CodePos {
  file: string;
  line: number;
}

export interface CodeType {
  id: string;
  name: string;
  kind: "struct" | "interface" | "named" | "alias";
  exported: boolean;
  doc?: string;
  underlying?: string;
  pos: CodePos;
  fields: { name: string; type: string; embedded?: boolean; exported: boolean }[];
  methods: CodeMethod[];
  source?: string;
}

export interface CodeMethod {
  id: string;
  name: string;
  sig: string;
  exported: boolean;
  pointer?: boolean;
  doc?: string;
  pos: CodePos;
  source?: string;
}

export type CodeFunc = Omit<CodeMethod, "pointer">;

export interface CodeRelation {
  from: string;
  to: string;
  kind: "implements" | "embeds" | "has" | "uses";
  label?: string;
}

export interface CodeStub {
  id: string;
  name: string;
  package: string;
  dir: string;
  component?: string;
  kind: CodeType["kind"];
  methods?: string[];
}

export interface CodeCall {
  from: string;
  to: string;
  line?: number;
  /** "rpc": a gRPC client method reaching the server method, over the network. */
  kind?: "rpc";
}

export interface CodeContract {
  proto: string;
  generated?: string;
  services: { name: string; rpcs: { name: string; request: string; response: string; clientStreams?: boolean; serverStreams?: boolean }[] }[];
  messages: string[];
}
