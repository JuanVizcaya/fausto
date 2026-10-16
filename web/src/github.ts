// Links from the Architecture tab into the repository's GitHub remote,
// at a user-picked branch. repo is the URL from Info.repo (e.g.
// "https://github.com/owner/repo"); nothing here is called when it's unset.

/** "https://github.com/owner/repo" -> "owner/repo", or null if it doesn't look like one. */
export function repoSlug(repo: string): string | null {
  const m = /^https?:\/\/github\.com\/([^/]+\/[^/]+)\/?$/.exec(repo);
  return m ? m[1] : null;
}

export function githubTreeUrl(repo: string, branch: string, dirPath: string): string {
  return `${repo}/tree/${encodeURIComponent(branch)}/${dirPath}`;
}

export function githubBlobUrl(repo: string, branch: string, filePath: string, line?: number): string {
  const url = `${repo}/blob/${encodeURIComponent(branch)}/${filePath}`;
  return line ? `${url}#L${line}` : url;
}

/** Best-effort branch list; [] on any failure (private repo, rate limit, offline). */
export async function fetchGithubBranches(repo: string): Promise<string[]> {
  const slug = repoSlug(repo);
  if (!slug) return [];
  try {
    const res = await fetch(`https://api.github.com/repos/${slug}/branches?per_page=100`);
    if (!res.ok) return [];
    const data = await res.json();
    if (!Array.isArray(data)) return [];
    return data.map((b) => b?.name).filter((n): n is string => typeof n === "string");
  } catch {
    return [];
  }
}
