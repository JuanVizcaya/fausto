import { useEffect, useState } from "react";
import { fetchGithubBranches } from "./github";

/** Picks the branch that "View on GitHub" links use. Session-only: the
 * choice lives in the caller's React state, not persisted anywhere. */
export function GithubBranchPicker({ repo, value, onChange }: { repo: string; value: string; onChange: (branch: string) => void }) {
  const [branches, setBranches] = useState<string[]>([]);

  useEffect(() => {
    setBranches([]);
    fetchGithubBranches(repo).then(setBranches);
  }, [repo]);

  if (branches.length === 0) {
    return (
      <input
        type="text"
        className="select"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="branch"
        aria-label="GitHub branch"
        title="GitHub branch for “View on GitHub” links"
      />
    );
  }
  const options = branches.includes(value) ? branches : [value, ...branches];
  return (
    <select className="select" value={value} onChange={(e) => onChange(e.target.value)} aria-label="GitHub branch">
      {options.map((b) => (
        <option key={b} value={b}>
          {b}
        </option>
      ))}
    </select>
  );
}
