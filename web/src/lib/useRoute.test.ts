import { describe, expect, test } from "bun:test";
import { parseRoute, routeToPath } from "./useRoute";

// The dashboard used to hold its location in React state alone: every page was
// "/", a reload dropped you back on the overview, and Back left the app. These
// two functions are what fixed that, and they are pure, so they are worth
// pinning — a regression here silently breaks every bookmark and shared link.

describe("parseRoute", () => {
  test("reads a project and a page", () => {
    expect(parseRoute("/p/abc123/board")).toEqual({
      projectId: "abc123",
      page: "board",
    });
  });

  test("a project with no page means its overview", () => {
    expect(parseRoute("/p/abc123")).toEqual({
      projectId: "abc123",
      page: "overview",
    });
  });

  test("docs belongs to no project", () => {
    expect(parseRoute("/docs")).toEqual({ projectId: null, page: "docs" });
  });

  test("the root has no project yet — the app resolves it to the first one", () => {
    expect(parseRoute("/")).toEqual({ projectId: null, page: "overview" });
  });

  test("an unknown page falls back to the overview rather than rendering nothing", () => {
    expect(parseRoute("/p/abc123/nosuchpage")).toEqual({
      projectId: "abc123",
      page: "overview",
    });
  });

  test("trailing slashes and doubled separators are tolerated", () => {
    expect(parseRoute("/p/abc123/board/")).toEqual({
      projectId: "abc123",
      page: "board",
    });
    expect(parseRoute("//p//abc123//room//")).toEqual({
      projectId: "abc123",
      page: "room",
    });
  });

  test("every page in the nav round-trips", () => {
    for (const page of [
      "overview",
      "board",
      "plans",
      "agents",
      "claims",
      "room",
      "notes",
      "activity",
    ] as const) {
      const path = routeToPath({ projectId: "p1", page });
      expect(parseRoute(path)).toEqual({ projectId: "p1", page });
    }
  });
});

describe("routeToPath", () => {
  test("the overview is the bare project path, not /overview", () => {
    expect(routeToPath({ projectId: "abc", page: "overview" })).toBe("/p/abc");
  });

  test("other pages are named", () => {
    expect(routeToPath({ projectId: "abc", page: "room" })).toBe("/p/abc/room");
  });

  test("docs ignores the project", () => {
    expect(routeToPath({ projectId: "abc", page: "docs" })).toBe("/docs");
  });

  test("no project resolves to the root", () => {
    expect(routeToPath({ projectId: null, page: "board" })).toBe("/");
  });
});
