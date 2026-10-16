import { useState } from "react";
import { EditButton } from "../../editor";
import type { Spec } from "../../types";
import { Empty, Section } from "../../ui";

/** The project's shared vocabulary, from domains.yaml. */
export function Glossary({ spec }: { spec: Spec }) {
  const [query, setQuery] = useState("");
  const glossary = spec.domains.glossary ?? [];
  const q = query.trim().toLowerCase();
  const shown = glossary.filter((t) => !q || t.term.toLowerCase().includes(q) || t.definition.toLowerCase().includes(q));
  return (
    <Section title="Glossary" aside={<EditButton kind="term" label="Add term" />}>
      {glossary.length > 0 && (
        <div className="filters glossary-search" role="search">
          <input
            type="search"
            className="search"
            placeholder="Search terms and definitions…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search the glossary"
          />
        </div>
      )}
      {glossary.length === 0 ? (
        <Empty>No terms defined.</Empty>
      ) : shown.length === 0 ? (
        <Empty>No terms match.</Empty>
      ) : (
        <dl className="glossary">
          {shown.map((term) => (
            <div key={term.term}>
              <dt>{term.term}</dt>
              <dd>
                {term.definition} <EditButton kind="term" id={term.term} />
              </dd>
            </div>
          ))}
        </dl>
      )}
    </Section>
  );
}
