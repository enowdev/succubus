/**
 * succubus plugin for OpenCode.
 *
 * OpenCode has no shell-hook system — its lifecycle events are only reachable
 * from a JS/TS plugin. This bridges them to the same `succubus hook` handler
 * every other tool uses, so OpenCode gets identical behaviour: automatic
 * registration, context injection, nagging, and blocking.
 *
 * Install:
 *   mkdir -p .opencode/plugin
 *   cp succubus.ts .opencode/plugin/
 *
 * Set SUCCUBUS_BIN if the binary is not on PATH.
 */

const BIN = process.env.SUCCUBUS_BIN ?? "succubus";

/** Tool names that modify files, and therefore need a claim. */
const MUTATING = new Set([
  "edit",
  "write",
  "multiedit",
  "patch",
  "apply_patch",
]);

interface HookResult {
  /** Text to surface to the agent, from an inject or a nag. */
  message?: string;
  /** True when succubus refused the edit. */
  blocked?: boolean;
  reason?: string;
}

/**
 * Runs `succubus hook <event>`, feeding it a payload shaped like the one the
 * other dialects send. Never throws: succubus being unavailable must not
 * interrupt the session.
 */
async function runHook(
  event: string,
  payload: Record<string, unknown>,
): Promise<HookResult> {
  try {
    const proc = Bun.spawn([BIN, "hook", event, "--dialect", "opencode"], {
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
      env: { ...process.env, SUCCUBUS_TOOL: "opencode" },
    });

    proc.stdin.write(JSON.stringify(payload));
    proc.stdin.end();

    // A hook that hangs would stall the agent, so cap it hard.
    const timer = setTimeout(() => proc.kill(), 5000);
    const [stdout, exitCode] = await Promise.all([
      new Response(proc.stdout).text(),
      proc.exited,
    ]);
    clearTimeout(timer);

    // Exit 2 is the block signal shared by every dialect.
    if (exitCode === 2) {
      return { blocked: true, reason: stdout.trim() || "held by another agent" };
    }
    if (!stdout.trim()) return {};

    const out = JSON.parse(stdout);
    if (out?.hookSpecificOutput?.permissionDecision === "deny") {
      return {
        blocked: true,
        reason: out.hookSpecificOutput.permissionDecisionReason ?? "conflict",
      };
    }
    return {
      message:
        out?.hookSpecificOutput?.additionalContext ?? out?.systemMessage ?? undefined,
    };
  } catch {
    // Daemon down, binary missing, malformed output — all non-fatal.
    return {};
  }
}

/** Pulls file paths out of whatever shape this tool's arguments take. */
function pathsOf(args: Record<string, unknown> | undefined): string[] {
  if (!args) return [];
  const out: string[] = [];
  for (const key of ["filePath", "file_path", "path", "notebook_path"]) {
    const v = args[key];
    if (typeof v === "string" && v) out.push(v);
  }
  const edits = args.edits;
  if (Array.isArray(edits)) {
    for (const e of edits) {
      const v = (e as Record<string, unknown>)?.file_path;
      if (typeof v === "string" && v) out.push(v);
    }
  }
  return [...new Set(out)];
}

export const SuccubusPlugin = async ({ directory, worktree }: any) => {
  const cwd = worktree ?? directory ?? process.cwd();
  let sessionKey = "";

  return {
    /** Register this session and pull in the shared state. */
    event: async ({ event }: any) => {
      if (event.type === "session.created" || event.type === "session.updated") {
        sessionKey = event.properties?.info?.id ?? sessionKey;
        const res = await runHook("SessionStart", {
          session_id: sessionKey,
          cwd,
        });
        if (res.message) {
          console.log(`[succubus]\n${res.message}`);
        }
      }

      // A finished session must not keep holding files.
      if (event.type === "session.idle" || event.type === "session.deleted") {
        await runHook("SessionEnd", { session_id: sessionKey, cwd });
      }
    },

    /** Refuse an edit when another live agent holds the file. */
    "tool.execute.before": async (input: any, output: any) => {
      const tool = String(input?.tool ?? "").toLowerCase();
      if (!MUTATING.has(tool)) return;

      const paths = pathsOf(output?.args);
      if (paths.length === 0) return;

      const res = await runHook("PreToolUse", {
        session_id: sessionKey,
        cwd,
        tool_name: input.tool,
        tool_input: output.args,
      });

      if (res.blocked) {
        // Throwing is how an OpenCode plugin refuses a tool call.
        throw new Error(`succubus: ${res.reason}`);
      }
    },

    /** Warn — and retroactively claim — when a file was edited unclaimed. */
    "tool.execute.after": async (input: any, output: any) => {
      const tool = String(input?.tool ?? "").toLowerCase();
      if (!MUTATING.has(tool)) return;

      const paths = pathsOf(output?.args);
      if (paths.length === 0) return;

      const res = await runHook("PostToolUse", {
        session_id: sessionKey,
        cwd,
        tool_name: input.tool,
        tool_input: output.args,
      });

      if (res.message) {
        console.log(`[succubus] ${res.message}`);
      }
    },
  };
};

export default SuccubusPlugin;
