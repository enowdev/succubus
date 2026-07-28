import type { ReactNode } from "react";

/**
 * A small markdown renderer for the docs pages.
 *
 * The content is our own, so it only needs to handle what we actually write:
 * headings, paragraphs, lists, GitHub tables, fenced code, blockquotes, rules,
 * and inline code / bold / links. That is a few dozen lines — far less weight
 * than pulling in a markdown library and a sanitizer.
 */

/** Renders inline spans: `code`, **bold**, [links](…). */
function inline(text: string, key: string): ReactNode {
  const parts = text.split(/(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\([^)]+\))/g);
  return parts.map((p, i) => {
    const k = `${key}-${i}`;
    if (p.startsWith("`") && p.endsWith("`") && p.length > 1) {
      return (
        <code key={k} className="mono">
          {p.slice(1, -1)}
        </code>
      );
    }
    if (p.startsWith("**") && p.endsWith("**") && p.length > 3) {
      return <b key={k}>{p.slice(2, -2)}</b>;
    }
    const link = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(p);
    if (link) {
      const [, label, href] = link;
      // In-repo links point at sections the dashboard already renders, so keep
      // the reader here instead of sending them to a 404.
      const internal = href.endsWith(".md") || href.startsWith("./");
      if (internal) return <span key={k}>{label}</span>;
      return (
        <a key={k} href={href} target="_blank" rel="noreferrer">
          {label}
        </a>
      );
    }
    return <span key={k}>{p}</span>;
  });
}

/** Turns a heading's text into a stable anchor id. */
export function slug(text: string): string {
  return text
    .toLowerCase()
    .replace(/[`*_[\]()]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
}

export interface Heading {
  text: string;
  id: string;
  /** H3s belonging to this H2, so the contents rail can collapse them. */
  children: { text: string; id: string }[];
}

/** Extracts H2s with their H3s nested, for the on-page contents rail. */
export function headingsOf(md: string): Heading[] {
  const out: Heading[] = [];
  let fenced = false;
  for (const line of md.split("\n")) {
    if (line.trimStart().startsWith("```")) {
      fenced = !fenced;
      continue;
    }
    if (fenced) continue;
    const m = /^(#{2,3})\s+(.*)$/.exec(line);
    if (!m) continue;

    const text = m[2].trim();
    const entry = { text, id: slug(text) };
    if (m[1].length === 2) {
      out.push({ ...entry, children: [] });
    } else if (out.length > 0) {
      out[out.length - 1].children.push(entry);
    } else {
      // An H3 before any H2 still deserves a top-level entry.
      out.push({ ...entry, children: [] });
    }
  }
  return out;
}

export function Markdown({ source }: { source: string }) {
  const lines = source.split("\n");
  const out: ReactNode[] = [];
  let i = 0;
  let key = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block.
    if (line.trimStart().startsWith("```")) {
      const lang = line.trim().slice(3).trim();
      const buf: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trimStart().startsWith("```")) {
        buf.push(lines[i]);
        i++;
      }
      i++; // closing fence
      out.push(
        <div key={key++} className="code-block">
          {lang && (
            <div className="code-head">
              <span className="mono dim">{lang}</span>
            </div>
          )}
          <pre className="code-body mono">{buf.join("\n")}</pre>
        </div>,
      );
      continue;
    }

    // Table: a | row followed by a |---| separator.
    if (
      line.trim().startsWith("|") &&
      i + 1 < lines.length &&
      /^\s*\|[\s:|-]+\|\s*$/.test(lines[i + 1])
    ) {
      const cells = (l: string) =>
        l
          .trim()
          .replace(/^\||\|$/g, "")
          .split("|")
          .map((c) => c.trim());
      const header = cells(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].trim().startsWith("|")) {
        rows.push(cells(lines[i]));
        i++;
      }
      out.push(
        <div key={key++} className="docs-table-wrap">
          <table className="docs-table">
            <thead>
              <tr>
                {header.map((h, hi) => (
                  <th key={hi}>{inline(h, `th${key}-${hi}`)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, ri) => (
                <tr key={ri}>
                  {r.map((c, ci) => (
                    <td key={ci}>{inline(c, `td${key}-${ri}-${ci}`)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }

    // Headings. H1 is dropped: the page header already shows the title.
    const h = /^(#{1,4})\s+(.*)$/.exec(line);
    if (h) {
      const level = h[1].length;
      const text = h[2].trim();
      if (level === 1) {
        i++;
        continue;
      }
      const id = slug(text);
      const Tag = (`h${level}` as "h2" | "h3" | "h4");
      out.push(
        <Tag key={key++} id={id} className={`docs-h${level}`}>
          {inline(text, `h${key}`)}
        </Tag>,
      );
      i++;
      continue;
    }

    // Horizontal rule.
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      out.push(<hr key={key++} className="docs-hr" />);
      i++;
      continue;
    }

    // Blockquote.
    if (line.trimStart().startsWith("> ")) {
      const buf: string[] = [];
      while (i < lines.length && lines[i].trimStart().startsWith(">")) {
        buf.push(lines[i].trimStart().replace(/^>\s?/, ""));
        i++;
      }
      out.push(
        <blockquote key={key++} className="docs-quote">
          {inline(buf.join(" "), `bq${key}`)}
        </blockquote>,
      );
      continue;
    }

    // Lists. Ordered and unordered are handled the same way; nesting is
    // rendered flat with an indent class, which is all our docs need.
    if (/^\s*([-*]|\d+\.)\s+/.test(line)) {
      const ordered = /^\s*\d+\./.test(line);
      const items: { text: string; nested: boolean }[] = [];
      while (i < lines.length && /^\s*([-*]|\d+\.)\s+/.test(lines[i])) {
        const indent = lines[i].length - lines[i].trimStart().length;
        let text = lines[i].trimStart().replace(/^([-*]|\d+\.)\s+/, "");
        i++;
        // Continuation lines belong to the item above.
        while (
          i < lines.length &&
          lines[i].trim() !== "" &&
          !/^\s*([-*]|\d+\.)\s+/.test(lines[i]) &&
          !/^#{1,4}\s/.test(lines[i]) &&
          !lines[i].trimStart().startsWith("```") &&
          !lines[i].trim().startsWith("|")
        ) {
          text += " " + lines[i].trim();
          i++;
        }
        items.push({ text, nested: indent >= 2 });
      }
      const List = ordered ? "ol" : "ul";
      out.push(
        <List key={key++} className="docs-list">
          {items.map((it, ii) => (
            <li key={ii} className={it.nested ? "nested" : undefined}>
              {inline(it.text, `li${key}-${ii}`)}
            </li>
          ))}
        </List>,
      );
      continue;
    }

    // Blank line.
    if (line.trim() === "") {
      i++;
      continue;
    }

    // Paragraph: gather until a blank line or the start of another block.
    const buf: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^#{1,4}\s/.test(lines[i]) &&
      !lines[i].trimStart().startsWith("```") &&
      !lines[i].trimStart().startsWith(">") &&
      !lines[i].trim().startsWith("|") &&
      !/^\s*([-*]|\d+\.)\s+/.test(lines[i]) &&
      !/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(lines[i])
    ) {
      buf.push(lines[i].trim());
      i++;
    }
    if (buf.length > 0) {
      out.push(
        <p key={key++} className="docs-p">
          {inline(buf.join(" "), `p${key}`)}
        </p>,
      );
    }
  }

  return <div className="docs-md">{out}</div>;
}
