import { useCallback, useEffect, useState } from "react";
import type { Page } from "../components/Sidebar";

/**
 * Keeps the visible page and project in the URL.
 *
 * Without this the dashboard held its location in React state alone: every
 * page was "/", a reload dropped you back on the overview, and the browser's
 * Back button left the app instead of going back a page. For something people
 * keep open in a tab and send links to, that is a daily annoyance — you cannot
 * bookmark the board or point a colleague at the room.
 *
 * The History API is enough here. The URL shape is two segments, and
 * react-router would be a dependency and a bundle cost for a parser that fits
 * in a few lines.
 *
 *   /                      overview of the first project
 *   /p/<projectId>         that project's overview
 *   /p/<projectId>/board   a specific page
 *   /docs                  documentation, which belongs to no project
 */

const PAGES: readonly Page[] = [
  "overview",
  "board",
  "plans",
  "agents",
  "claims",
  "room",
  "notes",
  "activity",
  "docs",
] as const;

export type Route = { projectId: string | null; page: Page };

function isPage(s: string): s is Page {
  return (PAGES as readonly string[]).includes(s);
}

/** Reads the current URL. Anything unrecognised falls back to the overview. */
export function parseRoute(pathname: string): Route {
  const parts = pathname.split("/").filter(Boolean);

  // /docs is deliberately outside a project: it is the same for all of them.
  if (parts[0] === "docs") return { projectId: null, page: "docs" };

  if (parts[0] === "p" && parts[1]) {
    const page = parts[2];
    return {
      projectId: parts[1],
      page: page && isPage(page) ? page : "overview",
    };
  }
  return { projectId: null, page: "overview" };
}

export function routeToPath(r: Route): string {
  if (r.page === "docs") return "/docs";
  if (!r.projectId) return "/";
  return r.page === "overview" ? `/p/${r.projectId}` : `/p/${r.projectId}/${r.page}`;
}

/**
 * The current route, plus a setter that writes to the address bar.
 *
 * `replace` is for corrections the user did not ask for — resolving "/" to the
 * first project, say. Those should not become Back-button stops, or Back walks
 * through states nobody navigated to.
 */
export function useRoute() {
  const [route, setRouteState] = useState<Route>(() => parseRoute(location.pathname));

  // Back and Forward change the URL without going through navigate().
  useEffect(() => {
    const onPop = () => setRouteState(parseRoute(location.pathname));
    addEventListener("popstate", onPop);
    return () => removeEventListener("popstate", onPop);
  }, []);

  const navigate = useCallback((next: Partial<Route>, replace = false) => {
    setRouteState((cur) => {
      const merged = { ...cur, ...next };
      const path = routeToPath(merged);
      if (path !== location.pathname) {
        history[replace ? "replaceState" : "pushState"]({}, "", path);
      }
      return merged;
    });
  }, []);

  return { route, navigate };
}
