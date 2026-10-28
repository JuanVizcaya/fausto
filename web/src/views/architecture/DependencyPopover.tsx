import type { Component, Dependency, Spec } from "../../types";
import { Code } from "../../ui";

export type Protocol = Dependency["protocol"];
export const protocols: Protocol[] = ["grpc", "contract", "http", "sql", "library"];

/** The protocol a new dependency on target most likely uses. */
export function suggestProtocol(target: Component): Protocol {
  switch (target.kind) {
    case "database":
      return "sql";
    case "library":
      return "library";
    case "service":
      return target.contracts?.some((ct) => ct.go) ? "contract" : "grpc";
    default:
      return "http";
  }
}

/** Why the layer rules forbid from depending on to, or "" if they allow it. */
export function layerProblem(spec: Spec, from: Component, to: Component, protocol: Protocol): string {
  if (from.layer === to.layer) {
    return protocol === "library" || protocol === "contract" ? "" : `Within the ${from.layer} layer, only library and contract dependencies are allowed.`;
  }
  const allowed = spec.system.layers.find((l) => l.id === from.layer)?.may_depend_on ?? [];
  return allowed.includes(to.layer) ? "" : `The ${from.layer} layer may not depend on the ${to.layer} layer; the layers check will fail.`;
}

/** The popover for a new or existing dependency, at wrap coordinates. */
export interface DepPopover {
  from: string;
  to: string;
  /** The current protocol for an existing dependency. */
  protocol?: Protocol;
  left: number;
  top: number;
  above: boolean;
}

/** Chooses the protocol of a new dependency, or changes or removes an
 * existing one. */
export function DependencyPopover({
  popover,
  spec,
  onSet,
  onClose,
}: {
  popover: DepPopover;
  spec: Spec;
  onSet: (protocol: Protocol | null) => void;
  onClose: () => void;
}) {
  const from = spec.system.components.find((c) => c.id === popover.from);
  const to = spec.system.components.find((c) => c.id === popover.to);
  if (!from || !to) return null;
  const current = popover.protocol;
  const suggested = current ?? suggestProtocol(to);
  const problem = layerProblem(spec, from, to, suggested);
  return (
    <div
      className={popover.above ? "dep-popover above" : "dep-popover"}
      role="dialog"
      aria-label={`Dependency from ${from.id} to ${to.id}`}
      style={{ left: popover.left, top: popover.top }}
      onKeyDown={(e) => e.key === "Escape" && onClose()}
    >
      <div className="dep-popover-head">
        <span>
          <Code>{from.id}</Code> → <Code>{to.id}</Code>
        </span>
        <button className="btn btn-ghost btn-small" onClick={onClose} aria-label="Close">
          ×
        </button>
      </div>
      <p className="muted">{current ? "Change the protocol or remove this dependency." : "Add a dependency via:"}</p>
      <div className="dep-protocols">
        {protocols.map((p) => (
          <button
            key={p}
            autoFocus={p === suggested}
            className={p === current ? "btn btn-small btn-primary" : p === suggested && !current ? "btn btn-small btn-primary" : "btn btn-small"}
            onClick={() => onSet(p)}
            title={layerProblem(spec, from, to, p) || undefined}
          >
            {p}
          </button>
        ))}
      </div>
      {problem && <p className="dep-warning">{problem}</p>}
      {current && (
        <button className="btn btn-small btn-danger" onClick={() => onSet(null)}>
          Remove dependency
        </button>
      )}
    </div>
  );
}
