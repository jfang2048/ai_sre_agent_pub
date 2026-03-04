#!/usr/bin/env node

import fs from 'node:fs/promises';
import path from 'node:path';

const DEVTOOLS_PORT = Number(process.env.CHROME_DEVTOOLS_PORT ?? 9224);
const UI_URL = process.env.UI_URL ?? 'http://127.0.0.1:8080';
const OUT_DIR = process.env.SCREENSHOT_DIR ?? 'docs/images';
const TIMEOUT_MS = Number(process.env.CAPTURE_TIMEOUT_MS ?? 30000);
const WARMUP_MS = Number(process.env.CAPTURE_WARMUP_MS ?? 4000);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function fetchJson(url, init) {
  const response = await fetch(url, init);
  if (!response.ok) {
    throw new Error(`request failed ${response.status} ${response.statusText}: ${url}`);
  }
  return response.json();
}

async function waitForDevtools(port, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      await fetchJson(`http://127.0.0.1:${port}/json/version`);
      return;
    } catch {
      await sleep(200);
    }
  }
  throw new Error(`devtools endpoint not ready on :${port}`);
}

class CDPClient {
  constructor(wsURL) {
    this.ws = new WebSocket(wsURL);
    this.nextID = 1;
    this.pending = new Map();
  }

  async open() {
    await new Promise((resolve, reject) => {
      const onOpen = () => {
        cleanup();
        resolve();
      };
      const onError = (err) => {
        cleanup();
        reject(err);
      };
      const cleanup = () => {
        this.ws.removeEventListener('open', onOpen);
        this.ws.removeEventListener('error', onError);
      };
      this.ws.addEventListener('open', onOpen);
      this.ws.addEventListener('error', onError);
    });

    this.ws.addEventListener('message', (event) => {
      let message;
      try {
        message = JSON.parse(String(event.data));
      } catch {
        return;
      }
      if (!message.id) {
        return;
      }
      const entry = this.pending.get(message.id);
      if (!entry) {
        return;
      }
      this.pending.delete(message.id);
      if (message.error) {
        entry.reject(new Error(message.error.message || 'cdp error'));
      } else {
        entry.resolve(message.result ?? {});
      }
    });
  }

  call(method, params = {}) {
    const id = this.nextID++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }

  async close() {
    try {
      this.ws.close();
    } catch {
      // ignore
    }
  }
}

async function newPage(port, targetURL) {
  return fetchJson(`http://127.0.0.1:${port}/json/new?${encodeURIComponent(targetURL)}`, {
    method: 'PUT',
  });
}

async function closePage(port, targetID) {
  try {
    await fetch(`http://127.0.0.1:${port}/json/close/${targetID}`);
  } catch {
    // ignore
  }
}

async function evalExpr(cdp, expression) {
  const result = await cdp.call('Runtime.evaluate', {
    expression,
    returnByValue: true,
    awaitPromise: true,
  });
  return result?.result?.value;
}

async function waitForSelector(cdp, selector, timeoutMs) {
  const escaped = JSON.stringify(selector);
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const ok = await evalExpr(cdp, `(() => Boolean(document.querySelector(${escaped})))()`);
    if (ok) {
      return;
    }
    await sleep(200);
  }
  throw new Error(`selector timeout: ${selector}`);
}

async function waitForText(cdp, text, timeoutMs) {
  const escaped = JSON.stringify(text);
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const ok = await evalExpr(
      cdp,
      `(() => (document.body?.innerText ?? '').includes(${escaped}))()`,
    );
    if (ok) {
      return;
    }
    await sleep(250);
  }
  throw new Error(`text timeout: ${text}`);
}

async function clickSelector(cdp, selector) {
  const escaped = JSON.stringify(selector);
  const ok = await evalExpr(
    cdp,
    `(() => {
      const element = document.querySelector(${escaped});
      if (!element) return false;
      element.click();
      return true;
    })()`,
  );
  if (!ok) {
    throw new Error(`click failed: ${selector}`);
  }
}

async function scrollToText(cdp, text) {
  const escaped = JSON.stringify(text);
  await evalExpr(
    cdp,
    `(() => {
      const nodes = Array.from(document.querySelectorAll('section,div,h2,h3'));
      const target = nodes.find((node) => (node.textContent ?? '').includes(${escaped}));
      if (!target) return false;
      target.scrollIntoView({ behavior: 'instant', block: 'start' });
      return true;
    })()`,
  );
}

async function capture(cdp, fileName, captureBeyondViewport = true) {
  const shot = await cdp.call('Page.captureScreenshot', {
    format: 'png',
    fromSurface: true,
    captureBeyondViewport,
  });
  await fs.writeFile(path.join(OUT_DIR, fileName), Buffer.from(shot.data, 'base64'));
}

async function setViewport(cdp, width, height) {
  await cdp.call('Emulation.setDeviceMetricsOverride', {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: false,
  });
}

async function main() {
  await fs.mkdir(OUT_DIR, { recursive: true });
  await waitForDevtools(DEVTOOLS_PORT, TIMEOUT_MS);

  const page = await newPage(DEVTOOLS_PORT, UI_URL);
  const cdp = new CDPClient(page.webSocketDebuggerUrl);
  await cdp.open();

  try {
    await cdp.call('Page.enable');
    await cdp.call('Runtime.enable');
    await cdp.call('Network.enable');

    await setViewport(cdp, 1920, 1400);
    await cdp.call('Page.navigate', { url: UI_URL });
    await waitForSelector(cdp, 'main', TIMEOUT_MS);
    await waitForSelector(cdp, 'button[title="Dashboard"]', TIMEOUT_MS);
    await sleep(WARMUP_MS);

    await waitForText(cdp, 'SRE Command Center', TIMEOUT_MS);
    await capture(cdp, 'dashboard.png');

    await clickSelector(cdp, 'button[title="Joint Risk"]');
    await sleep(900);
    await waitForText(cdp, 'Joint Risk', TIMEOUT_MS);
    await waitForText(cdp, 'Ranked Signals', TIMEOUT_MS);
    await capture(cdp, 'joint-risk.png');

    await clickSelector(cdp, 'button[title="RCA Workflow"]');
    await sleep(900);
    await waitForText(cdp, 'RCA Workflow', TIMEOUT_MS);
    await waitForText(cdp, 'Ranked Hypotheses', TIMEOUT_MS);
    await capture(cdp, 'rca.png');

    await clickSelector(cdp, 'button[title="Dashboard"]');
    await sleep(900);
    await waitForText(cdp, 'Native Log Explorer', TIMEOUT_MS);
    await setViewport(cdp, 1920, 1000);
    await scrollToText(cdp, 'Native Log Explorer');
    await sleep(700);
    await capture(cdp, 'logs.png', false);
  } finally {
    await cdp.close();
    await closePage(DEVTOOLS_PORT, page.id);
  }

  console.log(`Saved screenshots to ${OUT_DIR}`);
}

main().catch((err) => {
  console.error(err instanceof Error ? err.message : String(err));
  process.exit(1);
});
