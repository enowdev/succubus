/**
 * Capture the dashboard pages used in the README.
 *
 * Drives headless Chrome over the DevTools protocol rather than pulling in a
 * browser-automation dependency — the only thing needed is a Chrome that is
 * already installed.
 *
 *   node scripts/screenshot.mjs [baseURL] [outDir]
 *
 * The viewport is overridable, because the dashboard is meant to be mobile
 * first and a 1440px capture is exactly the width at which mobile problems are
 * invisible:
 *
 *   SHOT_WIDTH=375 SHOT_HEIGHT=812 node scripts/screenshot.mjs
 */
import { spawn } from "node:child_process";
import { mkdir, writeFile, rm } from "node:fs/promises";
import { setTimeout as sleep } from "node:timers/promises";

const BASE = process.argv[2] ?? "http://localhost:5273";
const OUT = process.argv[3] ?? "docs/screenshots";
const PORT = 9222;
const WIDTH = Number(process.env.SHOT_WIDTH ?? 1440);
const HEIGHT = Number(process.env.SHOT_HEIGHT ?? 900);

const CHROME =
  process.env.CHROME_PATH ??
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

/** Pages to capture: the nav item to click, and the file to write. */
const SHOTS = [
  { nav: null, file: "overview.png", settle: 1200 },
  { nav: "Board", file: "board.png" },
  { nav: "Agents", file: "agents.png" },
  { nav: "Room", file: "room.png" },
  { nav: "Claims", file: "claims.png" },
  { nav: "Plans", file: "plans.png" },
  { nav: "Activity", file: "activity.png" },
  { nav: "Docs", file: "docs.png", settle: 900 },
];

async function cdp() {
  const res = await fetch(`http://127.0.0.1:${PORT}/json/list`);
  const targets = await res.json();
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

/** Runs an expression in the page and returns its value. */
const evaluate = (send, expr) =>
  send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true })
    .then((r) => r.result?.value);

async function main() {
  await rm(OUT, { recursive: true, force: true });
  await mkdir(OUT, { recursive: true });

  const chrome = spawn(CHROME, [
    `--remote-debugging-port=${PORT}`,
    "--headless=new",
    `--window-size=${WIDTH},${HEIGHT}`,
    "--hide-scrollbars",
    "--force-device-scale-factor=2", // retina-sharp output
    "--no-first-run",
    "--no-default-browser-check",
    "--user-data-dir=/tmp/succubus-shot-profile",
    "about:blank",
  ]);
  chrome.on("error", (e) => {
    console.error("chrome:", e.message);
    process.exit(1);
  });

  // Wait for the debugging port rather than guessing at a delay.
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
    mobile: false,
  });

  await send("Page.navigate", { url: BASE });
  // The shell only renders once /api/overview resolves.
  for (let i = 0; i < 60; i++) {
    if (await evaluate(send, `!!document.querySelector('.sidebar')`)) break;
    await sleep(250);
  }
  await sleep(1500); // let SSE connect and the first paint settle

  for (const shot of SHOTS) {
    if (shot.nav) {
      const clicked = await evaluate(
        send,
        `(() => {
          const item = [...document.querySelectorAll('.nav-item')]
            .find(el => el.textContent.trim().startsWith(${JSON.stringify(shot.nav)}));
          if (!item) return false;
          item.click();
          return true;
        })()`,
      );
      if (!clicked) {
        console.warn(`  ! could not find nav item ${shot.nav}`);
        continue;
      }
    }
    await sleep(shot.settle ?? 700);

    const { data } = await send("Page.captureScreenshot", {
      format: "png",
      captureBeyondViewport: false,
    });
    await writeFile(`${OUT}/${shot.file}`, Buffer.from(data, "base64"));
    console.log(`  ✓ ${OUT}/${shot.file}`);
  }

  close();
  chrome.kill();
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
