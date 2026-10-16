import { useId, useState, type KeyboardEvent } from "react";
import { resolve } from "./schema";
import type { Schema } from "./types";

type Value = unknown;

interface FieldProps {
  name: string;
  schema: Schema;
  root: Schema;
  value: Value;
  onChange: (v: Value) => void;
  suggest: Record<string, string[]>;
  required?: boolean;
}

/** Fields whose text is long enough to deserve a text area. */
const longText = new Set(["description", "rationale", "scope", "notes", "definition", "goal", "statement", "mitigation"]);

const humanize = (name: string) => name.replace(/_/g, " ").replace(/^\w/, (c) => c.toUpperCase());

/** Renders a form for a JSON Schema object. Objects keep keys in schema
 * order; arrays of strings use a chip editor; arrays of objects repeat a
 * sub-form. */
export function SchemaForm({ schema, root, value, onChange, suggest }: Omit<FieldProps, "name" | "required">) {
  return <ObjectFields schema={resolve(schema, root)} root={root} value={value} onChange={onChange} suggest={suggest} />;
}

function ObjectFields({ schema, root, value, onChange, suggest }: Omit<FieldProps, "name" | "required">) {
  const obj = (value && typeof value === "object" && !Array.isArray(value) ? value : {}) as Record<string, Value>;
  const set = (key: string, v: Value) => {
    // Rebuild in schema order so the YAML keeps a predictable layout.
    const next: Record<string, Value> = {};
    for (const k of Object.keys(schema.properties ?? {})) {
      const val = k === key ? v : obj[k];
      if (val !== undefined) next[k] = val;
    }
    onChange(next);
  };
  return (
    <div className="form-fields">
      {Object.entries(schema.properties ?? {}).map(([key, sub]) => (
        <Field
          key={key}
          name={key}
          schema={sub}
          root={root}
          value={obj[key]}
          onChange={(v) => set(key, v)}
          suggest={suggest}
          required={schema.required?.includes(key)}
        />
      ))}
    </div>
  );
}

function Field(props: FieldProps) {
  const { name, root, value, onChange, suggest, required } = props;
  const schema = resolve(props.schema, root);
  const id = useId();
  const label = (
    <label className="field-label" htmlFor={id}>
      {humanize(name)}
      {required && <span className="req" aria-label="required">*</span>}
    </label>
  );
  const help = schema.description && <p className="field-help">{schema.description}</p>;

  if (schema.enum) {
    return (
      <div className="field">
        {label}
        <select id={id} value={(value as string) ?? ""} onChange={(e) => onChange(e.target.value || undefined)}>
          {(!required || !value) && <option value="">—</option>}
          {schema.enum.map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
        {help}
      </div>
    );
  }

  if (schema.type === "array") {
    const items = resolve(schema.items ?? {}, root);
    const list = Array.isArray(value) ? value : [];
    if (items.enum) {
      return (
        <fieldset className="field">
          <legend className="field-label">{humanize(name)}</legend>
          <div className="checks-row">
            {items.enum.map((o) => (
              <label key={o} className="check">
                <input
                  type="checkbox"
                  checked={list.includes(o)}
                  onChange={(e) => onChange(e.target.checked ? [...list, o] : list.filter((x) => x !== o))}
                />
                {o}
              </label>
            ))}
          </div>
          {help}
        </fieldset>
      );
    }
    if (items.type === "object" || items.properties) {
      return (
        <fieldset className="field">
          <legend className="field-label">
            {humanize(name)}
            {required && <span className="req">*</span>}
          </legend>
          {list.map((item, i) => (
            <div key={i} className="repeat">
              <ObjectFields
                schema={items}
                root={root}
                value={item}
                onChange={(v) => onChange(list.map((x, j) => (j === i ? v : x)))}
                suggest={suggest}
              />
              <button type="button" className="btn btn-small" onClick={() => onChange(list.filter((_, j) => j !== i))}>
                Remove
              </button>
            </div>
          ))}
          <button type="button" className="btn btn-small" onClick={() => onChange([...list, {}])}>
            Add {humanize(name).replace(/s$/, "").toLowerCase()}
          </button>
          {help}
        </fieldset>
      );
    }
    return (
      <div className="field">
        {label}
        <ChipInput id={id} values={list as string[]} onChange={onChange} options={suggest[name]} />
        {help}
      </div>
    );
  }

  if (schema.type === "boolean") {
    return (
      <label className="check check-field">
        <input id={id} type="checkbox" checked={value === true} onChange={(e) => onChange(e.target.checked)} />
        {humanize(name)}
      </label>
    );
  }

  if (schema.type === "object" || schema.properties) {
    return (
      <fieldset className="field">
        <legend className="field-label">{humanize(name)}</legend>
        <ObjectFields schema={schema} root={root} value={value} onChange={onChange} suggest={suggest} />
        {help}
      </fieldset>
    );
  }

  const listId = suggest[name] ? `${id}-list` : undefined;
  return (
    <div className="field">
      {label}
      {longText.has(name) ? (
        <textarea id={id} rows={3} value={(value as string) ?? ""} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <input
          id={id}
          value={(value as string) ?? ""}
          onChange={(e) => onChange(e.target.value)}
          list={listId}
          pattern={schema.pattern}
          autoComplete="off"
        />
      )}
      {listId && (
        <datalist id={listId}>
          {suggest[name].map((o) => (
            <option key={o} value={o} />
          ))}
        </datalist>
      )}
      {help}
    </div>
  );
}

function ChipInput({ id, values, onChange, options }: { id: string; values: string[]; onChange: (v: string[]) => void; options?: string[] }) {
  const [text, setText] = useState("");
  const add = () => {
    const parts = text.split(",").map((s) => s.trim()).filter((s) => s && !values.includes(s));
    if (parts.length) onChange([...values, ...parts]);
    setText("");
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      add();
    } else if (e.key === "Backspace" && !text && values.length) {
      onChange(values.slice(0, -1));
    }
  };
  const listId = options ? `${id}-list` : undefined;
  return (
    <div className="chip-input">
      {values.map((v) => (
        <span key={v} className="chip">
          {v}
          <button type="button" aria-label={`Remove ${v}`} onClick={() => onChange(values.filter((x) => x !== v))}>
            ×
          </button>
        </span>
      ))}
      <input
        id={id}
        value={text}
        onChange={(e) => {
          const v = e.target.value;
          // Picking a suggestion from the list adds it right away.
          if ((e.nativeEvent as InputEvent).inputType !== "insertText" && options?.includes(v) && !values.includes(v)) {
            onChange([...values, v]);
            setText("");
          } else {
            setText(v);
          }
        }}
        onKeyDown={onKey}
        onBlur={add}
        list={listId}
        placeholder={values.length ? "" : "Type and press Enter"}
        autoComplete="off"
      />
      {listId && (
        <datalist id={listId}>
          {options!.filter((o) => !values.includes(o)).map((o) => (
            <option key={o} value={o} />
          ))}
        </datalist>
      )}
    </div>
  );
}
