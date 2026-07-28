/**
 * Measure mobile layout problems, rather than photograph them.
 *
 * The dashboard is meant to be mobile first, and the two failures that matter
 * there are invisible in a desktop screenshot: content wider than the screen
 * (the page scrolls sideways), and tap targets too small to hit reliably.
 * Both are measurable, so this asserts on numbers instead of asking someone to
 * squint at a PNG.
 *
 *   node scripts/audit-mobile.mjs [baseURL] [projectId]
 *
 * Exits non-zero when a page has a problem, so it can gate a release.
 */
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";

const BASE = process.argv[2] ?? "http://localhost:5273";
const PID = process.argv[3] ?? "d30bd226e77c";
const PORT = 9223;

// iPhone SE — the narrowest screen still worth supporting. Anything that fits
// here fits everywhere.
const WIDTH = Number(process.env.SHOT_WIDTH ?? 375);
const HEIGHT = Number(process.env.SHOT_HEIGHT ?? 812);

// 44px is the long-standing minimum for a reliable touch target. Below it,
// people miss and hit the thing next to it.
const MIN_TAP = 44;

const CHROME =
  process.env.CHROME_PATH ??
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

const PAGES = [
  ["overview", `/p/${PID}`],
  ["board", `/p/${PID}/board`],
  ["agents", `/p/${PID}/agents`],
  ["room", `/p/${PID}/room`],
  ["claims", `/p/${PID}/claims`],
  ["plans", `/p/${PID}/plans`],
  ["activity", `/p/${PID}/activity`],
  ["decisions", `/p/${PID}/decisions`],
  ["docs", `/docs`],
];

async function cdp() {
  const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const page = targets.find((t) => t.type === "page");
  if (!page) throw new Error("no page target");

  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((ok, bad) => {
    ws.onopen = ok;
    ws.onerror = bad;
  });

  let id = 0;
  const pending = new Map();
  ws.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
    }
  };

  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const msgId = ++id;
      pending.set(msgId, { resolve, reject });
      ws.send(JSON.stringify({ id: msgId, method, params }));
    });

  return { send, close: () => ws.close() };
}

const evaluate = (send, expr) =>
  send("Runtime.evaluate", {
    expression: expr,
    awaitPromise: true,
    returnByValue: true,
  }).then((r) => r.result?.value);

/** Runs in the page: find what overflows, and what is too small to tap. */
const PROBE = `(() => {
  const vw = document.documentElement.clientWidth;
  const out = { vw, scrollW: document.documentElement.scrollWidth, overflow: [], smallTaps: [], errors: [] };

  // Elements sticking out past the right edge. Report the widest few, and only
  // ones the user can actually see.
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') continue;
    if (r.right > vw + 1) {
      out.overflow.push({
        tag: el.tagName.toLowerCase(),
        cls: (el.className && String(el.className).slice(0, 40)) || '',
        right: Math.round(r.right),
        over: Math.round(r.right - vw),
      });
    }
  }
  out.overflow.sort((a, b) => b.over - a.over);
  out.overflow = out.overflow.slice(0, 5);

  // Interactive things too small to hit.
  //
  // The closed nav drawer is translated off-screen, not hidden, so computed
  // style still calls it visible. Reporting its contents as problems the user
  // can see is how a tool trains you to ignore it — skip anything sitting
  // entirely outside the viewport.
  for (const el of document.querySelectorAll('button, a, [role=button], input, select, summary')) {
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    if (r.right <= 0 || r.left >= vw || r.bottom <= 0) continue;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') continue;
    if (r.height < ${MIN_TAP} || r.width < ${MIN_TAP}) {
      // A control can be visually small but still easy to hit: the usual trick
      // is an ::after that extends the touch area without changing how the
      // button looks. Ask the document what is actually at the point rather
      // than trusting the box, or the fix reads as a failure.
      const midY = r.top + r.height / 2;
      const midX = r.left + r.width / 2;
      // elementFromPoint returns the topmost element, which for an extended
      // hit area is the button itself (the ::after is not a separate node) —
      // but for a small icon it is often a child <svg>. Walk up rather than
      // testing identity, or every icon button reads as unreachable.
      const hits = (x, y) => {
        let at = document.elementFromPoint(x, y);
        while (at) {
          if (at === el) return true;
          at = at.parentElement;
        }
        return false;
      };
      const padX = Math.max(0, (${MIN_TAP} - r.width) / 2);
      const padY = Math.max(0, (${MIN_TAP} - r.height) / 2);
      const reachable =
        (r.width >= ${MIN_TAP} || (hits(r.left - padX + 1, midY) && hits(r.right + padX - 1, midY))) &&
        (r.height >= ${MIN_TAP} || (hits(midX, r.top - padY + 1) && hits(midX, r.bottom + padY - 1)));
      if (reachable) continue;

      out.smallTaps.push({
        tag: el.tagName.toLowerCase(),
        text: (el.textContent || '').trim().slice(0, 24) ||
              el.title || el.getAttribute('aria-label') || '',
        w: Math.round(r.width),
        h: Math.round(r.height),
      });
    }
  }
  out.smallTaps = out.smallTaps.slice(0, 8);
  return out;
})()`;

async function main() {
  const chrome = spawn(CHROME, [
    `--remote-debugging-port=${PORT}`,
    "--headless=new",
    `--window-size=${WIDTH},${HEIGHT}`,
    "--hide-scrollbars",
    "--no-first-run",
    "--no-default-browser-check",
    "--user-data-dir=/tmp/succubus-audit-profile",
    "about:blank",
  ]);
  chrome.on("error", (e) => {
    console.error("chrome:", e.message);
    process.exit(1);
  });

  for (let i = 0; i < 40; i++) {
    try {
      await fetch(`http://127.0.0.1:${PORT}/json/version`);
      break;
    } catch {
      await sleep(250);
    }
  }

  const { send, close } = await cdp();
  await send("Page.enable");
  await send("Runtime.enable");
  await send("Emulation.setDeviceMetricsOverride", {
    width: WIDTH,
    height: HEIGHT,
    deviceScaleFactor: 2,
    mobile: true,
  });

  console.log(`\n  mobile audit @ ${WIDTH}x${HEIGHT}`);
  console.log("  " + "-".repeat(58) + "\n");

  let problems = 0;

  for (const [name, path] of PAGES) {
    await send("Page.navigate", { url: BASE + path });
    // Wait for content rather than a fixed delay: these pages render after
    // their fetch resolves.
    for (let i = 0; i < 40; i++) {
      const ready = await evaluate(send, `document.body && document.body.innerText.trim().length > 40`);
      if (ready) break;
      await sleep(200);
    }
    await sleep(600);

    const r = await evaluate(send, PROBE);
    if (!r) {
      console.log(`  ${name.padEnd(10)} — probe failed`);
      problems++;
      continue;
    }

    const scrolls = r.scrollW > r.vw + 1;
    const flags = [];
    if (scrolls) flags.push(`scrolls sideways (${r.scrollW}px > ${r.vw}px)`);
    if (r.smallTaps.length) flags.push(`${r.smallTaps.length} tap target(s) < ${MIN_TAP}px`);

    if (flags.length === 0) {
      console.log(`  ${name.padEnd(10)} ok`);
      continue;
    }

    problems++;
    console.log(`  ${name.padEnd(10)} ${flags.join(", ")}`);
    for (const o of r.overflow) {
      console.log(`      overflow  <${o.tag}${o.cls ? " ." + o.cls.split(" ")[0] : ""}> +${o.over}px`);
    }
    for (const t of r.smallTaps) {
      console.log(`      small tap <${t.tag}> ${t.w}x${t.h}  "${t.text}"`);
    }
  }

  console.log("\n  " + "-".repeat(58));
  console.log(problems === 0 ? "  no mobile layout problems\n" : `  ${problems} page(s) with problems\n`);

  close();
  chrome.kill();
  process.exit(problems === 0 ? 0 : 1);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
