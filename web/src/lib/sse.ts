import { useEffect, useRef, useState } from "react";
import type { EventRecord } from "./types";

export type StreamStatus = "connecting" | "open" | "closed";

/**
 * Every event name the daemon emits, mirroring the Ev* constants in
 * internal/store/model.go.
 *
 * Named SSE events never reach onmessage, so anything missing here is silently
 * not live — the page simply stops updating for that kind of change, with no
 * error anywhere. Add to this list whenever the server gains an event.
 */
export const EVENT_TYPES = [
  "agent.registered", "agent.left", "agent.heartbeat",
  "plan.created", "plan.updated", "plan.deleted",
  "task.created", "task.updated", "task.deleted", "task.moved",
  "claim.granted", "claim.denied", "claim.released", "claim.expired",
  "decision.created", "handoff", "report",
  "room.message", "room.resolved",
] as const;

interface Options {
  onEvent?: (ev: EventRecord) => void;
}

/**
 * Subscribes to the daemon's SSE feed for one project.
 *
 * The browser's EventSource reconnects on its own and replays the Last-Event-ID
 * header, which the server honours — so a dropped connection recovers the
 * events it missed rather than silently diverging.
 */
export function useEventStream(projectId: string | undefined, opts: Options = {}) {
  const [status, setStatus] = useState<StreamStatus>("connecting");
  const [lastEvent, setLastEvent] = useState<EventRecord | null>(null);
  // Keep the callback in a ref so a new function identity does not tear down
  // and re-open the stream on every render.
  const onEvent = useRef(opts.onEvent);
  onEvent.current = opts.onEvent;

  useEffect(() => {
    if (!projectId) return;
    setStatus("connecting");

    const es = new EventSource(`/api/projects/${projectId}/stream`);

    es.onopen = () => setStatus("open");
    es.onerror = () => {
      // EventSource retries automatically; reflect the gap in the UI.
      setStatus(es.readyState === EventSource.CLOSED ? "closed" : "connecting");
    };

    const handle = (e: MessageEvent) => {
      try {
        const ev = JSON.parse(e.data) as EventRecord;
        setLastEvent(ev);
        onEvent.current?.(ev);
      } catch {
        // Malformed frame: ignore rather than kill the stream.
      }
    };

    for (const t of EVENT_TYPES) es.addEventListener(t, handle);
    es.onmessage = handle;

    return () => {
      for (const t of EVENT_TYPES) es.removeEventListener(t, handle);
      es.close();
    };
  }, [projectId]);

  return { status, lastEvent };
}
