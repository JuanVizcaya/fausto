import { useEffect } from "react";
import { EditButton } from "../../editor";
import type { Spec, Technology } from "../../types";
import { Section } from "../../ui";

const rings: { status: Technology["status"]; label: string; hint: string }[] = [
  { status: "adopt", label: "Adopt", hint: "The default choice." },
  { status: "trial", label: "Trial", hint: "In limited use; may change." },
  { status: "assess", label: "Assess", hint: "Worth exploring; not in use yet." },
  { status: "hold", label: "Hold", hint: "Don't start new work with it." },
];

/** The technologies by adoption status, and the components using each. */
export function TechRadar({ spec, selected }: { spec: Spec; selected?: string }) {
  // Links to a technology land here; bring its card into view.
  useEffect(() => {
    if (selected) document.getElementById(`tech-${selected}`)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [selected]);
  const usedBy = (id: string) => spec.system.components.filter((c) => c.tech?.includes(id));
  return (
    <Section title="Technology radar" aside={<EditButton kind="technology" label="Add technology" />}>
      <div className="radar">
        {rings.map((ring) => {
          const techs = spec.tech.technologies.filter((t) => t.status === ring.status);
          return (
            <div key={ring.status} className={`ring ring-${ring.status}`}>
              <header>
                <h3>{ring.label}</h3>
                <span className="muted">{ring.hint}</span>
              </header>
              {techs.length === 0 && <p className="empty">None.</p>}
              {techs.map((t) => (
                <article key={t.id} id={`tech-${t.id}`} className={t.id === selected ? "card selected" : "card"}>
                  <div className="card-head">
                    <h4>{t.name}</h4>
                    <span className="aside-row">
                      <span className="muted">{t.category}</span>
                      <EditButton kind="technology" id={t.id} />
                    </span>
                  </div>
                  {t.notes && <p>{t.notes}</p>}
                  <p className="chips">
                    {usedBy(t.id).map((c) => (
                      <a key={c.id} href={`#/architecture/${c.id}`} className="chip">
                        {c.name}
                      </a>
                    ))}
                    {usedBy(t.id).length === 0 && <span className="muted">Not used by any component.</span>}
                  </p>
                </article>
              ))}
            </div>
          );
        })}
      </div>
    </Section>
  );
}
