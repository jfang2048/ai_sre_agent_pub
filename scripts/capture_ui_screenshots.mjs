#!/usr/bin/env node

import fs from 'node:fs/promises';
import path from 'node:path';

const DEVTOOLS_PORT = Number(process.env.CHROME_DEVTOOLS_PORT ?? 9223);
const UI_URL = process.env.UI_URL ?? 'http://127.0.0.1:8080/ui';
const OUT_DIR = process.env.SCREENSHOT_DIR ?? 'screenshot';
const TIMEOUT_MS = Number(process.env.CAPTURE_TIMEOUT_MS ?? 30000);
const LIVE_WAIT_MS = Number(process.env.CAPTURE_LIVE_WAIT_MS ?? 45000);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function fetchJson(url, init) {
  const res = await fetch(url, init);
  if (!res.ok) {
    throw new Error(`request failed ${res.status} ${res.statusText}: ${url}`);
  }
  return res.json();
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
  constructor(wsUrl) {
    this.ws = new WebSocket(wsUrl);
    this.nextId = 1;
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

    this.ws.addEventListener('message', (evt) => {
      let msg;
      try {
        msg = JSON.parse(String(evt.data));
      } catch {
        return;
      }
      if (!msg.id) {
        return;
      }
      const pending = this.pending.get(msg.id);
      if (!pending) {
        return;
      }
      this.pending.delete(msg.id);
      if (msg.error) {
        pending.reject(new Error(msg.error.message || 'cdp error'));
      } else {
        pending.resolve(msg.result ?? {});
      }
    });
  }

  call(method, params = {}) {
    const id = this.nextId++;
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

async function newPage(port, targetUrl) {
  return fetchJson(
    `http://127.0.0.1:${port}/json/new?${encodeURIComponent(targetUrl)}`,
    { method: 'PUT' },
  );
}

async function closePage(port, targetId) {
  try {
    await fetch(`http://127.0.0.1:${port}/json/close/${targetId}`);
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
    const ok = await evalExpr(
      cdp,
      `(() => Boolean(document.querySelector(${escaped})))()`,
    );
    if (ok) {
      return;
    }
    await sleep(200);
  }
  throw new Error(`selector timeout: ${selector}`);
}

async function waitForCondition(cdp, name, expression, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const ok = await evalExpr(cdp, expression);
    if (ok) {
      return;
    }
    await sleep(300);
  }
  throw new Error(`condition timeout: ${name}`);
}

async function clickSelector(cdp, selector) {
  const escaped = JSON.stringify(selector);
  const ok = await evalExpr(
    cdp,
    `(() => {
      const el = document.querySelector(${escaped});
      if (!el) return false;
      el.click();
      return true;
    })()`,
  );
  if (!ok) {
    throw new Error(`click failed: ${selector}`);
  }
}

async function setViewport(cdp, width, height) {
  await cdp.call('Emulation.setDeviceMetricsOverride', {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: false,
  });
}

async function saveShot(cdp, filePath, opts = {}) {
  const shot = await cdp.call('Page.captureScreenshot', {
    format: 'png',
    fromSurface: true,
    captureBeyondViewport: opts.captureBeyondViewport ?? true,
  });
  await fs.writeFile(filePath, Buffer.from(shot.data, 'base64'));
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
    await waitForSelector(cdp, 'button[title=\"Dashboard\"]', TIMEOUT_MS);
    await waitForSelector(cdp, 'button[title=\"Metric Trends\"]', TIMEOUT_MS);

    await waitForCondition(
      cdp,
      'dashboard live metrics',
      `(() => {
        const text = document.body?.innerText ?? '';
        if (text.includes('Awaiting trend samples')) return false;
        const values = Array.from(document.querySelectorAll('div.text-xl.font-semibold'))
          .map((el) => (el.textContent ?? '').replace(/[^0-9.+-]/g, ''))
          .map((raw) => Number.parseFloat(raw))
          .filter((value) => Number.isFinite(value));
        const nonZero = values.filter((value) => value > 0.05).length;
        return nonZero >= 2;
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(800);
    const dashboardLive = path.join(OUT_DIR, 'screenshot_ui_dashboard_live.png');
    await saveShot(cdp, dashboardLive);
    // Keep legacy README path stable.
    await fs.copyFile(dashboardLive, path.join(OUT_DIR, 'screenshot_ui_web.png'));

    await clickSelector(cdp, 'button[title=\"Metric Trends\"]');
    await waitForSelector(cdp, 'div.text-lg.font-semibold', TIMEOUT_MS);
    await waitForCondition(
      cdp,
      'metric trends curves',
      `(() => {
        const text = document.body?.innerText ?? '';
        const hasSamples = /Samples:\\s*[1-9]/.test(text);
        const curveCount = document.querySelectorAll('.recharts-line-curve').length;
        return hasSamples && curveCount >= 2;
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(1000);
    await saveShot(cdp, path.join(OUT_DIR, 'screenshot_ui_trends_live.png'));

    await evalExpr(
      cdp,
      `(() => {
        const button = Array.from(document.querySelectorAll('button'))
          .find((node) => (node.textContent ?? '').includes('Show processes'));
        if (button) button.click();
        return true;
      })()`,
    );
    await waitForSelector(cdp, '#resource-breakdown-panel', TIMEOUT_MS);
    await evalExpr(cdp, 'document.getElementById("resource-breakdown-panel")?.scrollIntoView({ behavior: "instant", block: "start" });');
    await sleep(1200);
    await saveShot(cdp, path.join(OUT_DIR, 'screenshot_ui_trends_breakdown.png'));

    await clickSelector(cdp, 'button[title=\"Dashboard\"]');
    await sleep(1500);
    await setViewport(cdp, 1920, 1800);
    await saveShot(cdp, path.join(OUT_DIR, 'screenshot_ui_dashboard_full.png'));
  } finally {
    await cdp.close();
    await closePage(DEVTOOLS_PORT, page.id);
  }
}

main().catch((err) => {
  console.error(err instanceof Error ? err.message : String(err));
  process.exitCode = 1;
});
