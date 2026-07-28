import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { BookOpen, Check, ChevronRight, Copy } from "lucide-react";
import { api } from "../lib/api";
import type { DocSection } from "../lib/types";
import { Markdown, headingsOf, slug } from "../components/Markdown";
import { Empty, PageHead, Panel, Skeleton } from "../components/ui";

export function Docs() {
  const [sections, setSections] = useState<DocSection[] | null>(null);
  const [active, setActive] = useState<string>("SETUP");
  const [body, setBody] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const scroller = useRef<HTMLDivElement>(null);

  useEffect(() => {
    api
      .docsList()
      .then((list) => {
        setSections(list);
        if (list.length > 0 && !list.some((s) => s.id === active)) {
          setActive(list[0].id);
        }
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
    // Only on mount: the section list does not change while the page is open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api
      .docsSection(active)
      .then((md) => {
        if (cancelled) return;
        setBody(md);
        setError(null);
        scroller.current?.scrollTo({ top: 0 });
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [active]);

  const headings = useMemo(() => headingsOf(body), [body]);
  const current = sections?.find((s) => s.id === active);

  // Which H2 groups are expanded in the contents rail, and which heading the
  // reader is currently inside.
  const [openGroups, setOpenGroups] = useState<Set<string>>(new Set());
  const [activeHeading, setActiveHeading] = useState<string>("");

  // A new section starts collapsed except for its first group — a long page
  // with every group open is the flat list we are trying to get away from.
  useEffect(() => {
    setOpenGroups(headings.length > 0 ? new Set([headings[0].id]) : new Set());
    setActiveHeading(headings.length > 0 ? headings[0].id : "");
  }, [headings]);

  // Track the heading nearest the top of the reading pane. On narrow screens
  // the pane does not scroll — the page does — so listen on both.
  useEffect(() => {
    const el = scroller.current;
    if (!el || headings.length === 0) return;

    const ids = headings.flatMap((h) => [h.id, ...h.children.map((c) => c.id)]);
    let frame = 0;

    const onScroll = () => {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        const top = el.getBoundingClientRect().top;
        let best = ids[0];
        for (const id of ids) {
          const node = document.getElementById(id);
          if (!node) continue;
          if (node.getBoundingClientRect().top - top <= 80) best = id;
          else break;
        }
        setActiveHeading(best);
        // Reveal the group containing whatever we scrolled into.
        const parent = headings.find(
          (h) => h.id === best || h.children.some((c) => c.id === best),
        );
        if (parent) {
          setOpenGroups((prev) => (prev.has(parent.id) ? prev : new Set(prev).add(parent.id)));
        }
      });
    };

    const page = el.closest(".content");
    el.addEventListener("scroll", onScroll, { passive: true });
    page?.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      el.removeEventListener("scroll", onScroll);
      page?.removeEventListener("scroll", onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [headings]);

  const jump = useCallback((id: string) => {
    const node = document.getElementById(id);
    const el = scroller.current;
    if (!node) return;

    // When the pane owns the scroll, scrollIntoView would align to the viewport
    // rather than to the pane — compute the offset within the pane instead.
    if (el && el.scrollHeight > el.clientHeight) {
      const delta = node.getBoundingClientRect().top - el.getBoundingClientRect().top;
      el.scrollTo({ top: el.scrollTop + delta - 12, behavior: "smooth" });
    } else {
      node.scrollIntoView({ behavior: "smooth", block: "start" });
    }
    setActiveHeading(id);
  }, []);

  function toggleGroup(id: string) {
    setOpenGroups((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  /** The one thing worth copying out of the docs: the agent contract. */
  const contract = `Before doing anything else, call succubus_register to adopt your identity in this project.
You will be given a name (for example ORION) — that is who you are here.

Then call succubus_context to read the active plan, your tasks, and the files other
agents are holding. Before editing any file, call succubus_claim_files with its path.
If a file is held by another agent, do not edit it.

Release files with succubus_release_files when you are done, and record progress with
succubus_report.`;

  function copyContract() {
    navigator.clipboard.writeText(contract).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }

  return (
    <>
      <PageHead title="Docs" id={current?.title} />

      <div className="docs-layout">
        <nav className="docs-nav" aria-label="Documentation sections">
          <div className="nav-label" style={{ paddingTop: 0 }}>
            Sections
          </div>
          {!sections ? (
            <Skeleton rows={4} />
          ) : (
            sections.map((s) => (
              <div
                key={s.id}
                className={`docs-nav-item ${active === s.id ? "active" : ""}`}
                onClick={() => setActive(s.id)}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    setActive(s.id);
                  }
                }}
                title={s.summary}
              >
                {s.title}
              </div>
            ))
          )}

          {/* Contents for the section being read: H2 groups that expand to
              reveal their H3s, so a long page stays scannable. */}
          {headings.length > 1 && (
            <>
              <div className="nav-label" style={{ paddingTop: 16 }}>
                On this page
              </div>
              <div className="docs-toc">
                {headings.map((h) => {
                  const expanded = openGroups.has(h.id);
                  const inGroup =
                    activeHeading === h.id || h.children.some((c) => c.id === activeHeading);
                  return (
                    <div key={h.id} className="docs-toc-group">
                      <div
                        className={`docs-toc-item ${inGroup ? "current" : ""}`}
                        onClick={() => jump(h.id)}
                        role="button"
                        tabIndex={0}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault();
                            jump(h.id);
                          }
                        }}
                      >
                        {h.children.length > 0 ? (
                          <button
                            className={`tree-caret ${expanded ? "open" : ""}`}
                            onClick={(e) => {
                              e.stopPropagation();
                              toggleGroup(h.id);
                            }}
                            aria-label={expanded ? "Collapse" : "Expand"}
                            aria-expanded={expanded}
                            tabIndex={-1}
                          >
                            <ChevronRight size={11} strokeWidth={2} />
                          </button>
                        ) : (
                          <span className="docs-toc-spacer" />
                        )}
                        <span className="truncate">{h.text}</span>
                      </div>

                      {expanded && h.children.length > 0 && (
                        <div className="docs-toc-children">
                          {h.children.map((c) => (
                            <div
                              key={c.id}
                              className={`docs-toc-item sub ${activeHeading === c.id ? "current" : ""}`}
                              onClick={() => jump(c.id)}
                              role="button"
                              tabIndex={0}
                              onKeyDown={(e) => {
                                if (e.key === "Enter" || e.key === " ") {
                                  e.preventDefault();
                                  jump(c.id);
                                }
                              }}
                            >
                              <span className="truncate">{c.text}</span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </nav>

        <Panel className="docs-panel" flush>
          <div className="docs-scroll" ref={scroller}>
            {error ? (
              <div style={{ padding: 15 }}>
                <div className="error-state">{error}</div>
              </div>
            ) : loading ? (
              <div style={{ padding: 18 }}>
                <Skeleton rows={10} />
              </div>
            ) : !body ? (
              <Empty
                icon={<BookOpen size={28} strokeWidth={1.5} />}
                title="Nothing here"
                desc="This section has no content."
              />
            ) : (
              <div className="docs-body">
                {active === "SETUP" && (
                  <div className="code-block" style={{ marginBottom: 20 }}>
                    <div className="code-head">
                      <span className="mono dim">
                        paste this into any agent that has no hook support
                      </span>
                      <button className="copy-btn" onClick={copyContract}>
                        {copied ? <Check size={12} /> : <Copy size={12} />}
                        {copied ? "copied" : "copy"}
                      </button>
                    </div>
                    <pre className="code-body mono">{contract}</pre>
                  </div>
                )}
                <Markdown source={body} />
              </div>
            )}
          </div>
        </Panel>
      </div>
    </>
  );
}

export { slug };
