#!/usr/bin/env node

import fs from 'node:fs/promises';
import path from 'node:path';

const DEVTOOLS_PORT = Number(process.env.CHROME_DEVTOOLS_PORT ?? 9224);
const UI_URL = process.env.UI_URL ?? 'http://127.0.0.1:8080';
const OUT_DIR = process.env.SCREENSHOT_DIR ?? 'docs/images';
const TIMEOUT_MS = Number(process.env.CAPTURE_TIMEOUT_MS ?? 30000);
const LIVE_WAIT_MS = Number(process.env.CAPTURE_LIVE_WAIT_MS ?? 45000);
const WARMUP_MS = Number(process.env.CAPTURE_WARMUP_MS ?? 12000);

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
    const ok = await evalExpr(cdp, `(() => Boolean(document.querySelector(${escaped})))()`);
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

async function clickButtonByText(cdp, text, scopeSelector = '') {
  const escapedText = JSON.stringify(text);
  const escapedScope = JSON.stringify(scopeSelector);
  const ok = await evalExpr(
    cdp,
    `(() => {
      const root = ${escapedScope} ? document.querySelector(${escapedScope}) : document;
      if (!root) return false;
      const buttons = Array.from(root.querySelectorAll('button'));
      const target = buttons.find((node) => {
        const label = (node.textContent ?? '').trim();
        return label === ${escapedText} || label.includes(${escapedText});
      });
      if (!target) return false;
      target.click();
      return true;
    })()`,
  );
  if (!ok) {
    throw new Error(`button click failed: ${text}`);
  }
}

async function scrollSelectorIntoView(cdp, selector) {
  const escaped = JSON.stringify(selector);
  await evalExpr(
    cdp,
    `(() => {
      const element = document.querySelector(${escaped});
      if (!element) return false;
      element.scrollIntoView({ behavior: 'instant', block: 'start' });
      return true;
    })()`,
  );
}

async function setViewport(cdp, width, height) {
  await cdp.call('Emulation.setDeviceMetricsOverride', {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: false,
  });
}

async function capture(cdp, fileName, captureBeyondViewport = true) {
  const shot = await cdp.call('Page.captureScreenshot', {
    format: 'png',
    fromSurface: true,
    captureBeyondViewport,
  });
  await fs.writeFile(path.join(OUT_DIR, fileName), Buffer.from(shot.data, 'base64'));
}

async function navigateToPage(cdp, title) {
  await clickSelector(cdp, `button[title="${title}"]`);
  await sleep(900);
}

async function waitForDashboardReady(cdp) {
  await waitForCondition(
    cdp,
    'dashboard live metrics',
    `(() => {
      const text = document.body?.innerText ?? '';
      if (!text.includes('SRE Command Center')) return false;
      if (text.includes('Awaiting trend samples')) return false;
      const values = Array.from(document.querySelectorAll('div.text-xl.font-semibold'))
        .map((el) => (el.textContent ?? '').replace(/[^0-9.+-]/g, ''))
        .map((raw) => Number.parseFloat(raw))
        .filter((value) => Number.isFinite(value));
      return values.filter((value) => value > 0.05).length >= 2;
    })()`,
    LIVE_WAIT_MS,
  );
}

async function waitForMetricTrendsReady(cdp) {
  await waitForCondition(
    cdp,
    'metric trends',
    `(() => {
      const text = document.body?.innerText ?? '';
      const hasSamples = /Samples:\\s*[1-9]/.test(text);
      const curveCount = document.querySelectorAll('.recharts-line-curve').length;
      return text.includes('Metric Trends') && hasSamples && curveCount >= 2;
    })()`,
    LIVE_WAIT_MS,
  );
}

async function openResourceBreakdown(cdp) {
  await clickButtonByText(cdp, 'Show processes');
  await waitForSelector(cdp, '#resource-breakdown-panel', TIMEOUT_MS);
  await waitForCondition(
    cdp,
    'resource breakdown ready',
    `(() => {
      const root = document.querySelector('#resource-breakdown-panel');
      if (!root) return false;
      const text = root.textContent ?? '';
      if (text.includes('Loading per-process rankings...')) return false;
      return root.querySelectorAll('tbody tr').length > 0;
    })()`,
    LIVE_WAIT_MS,
  );
}

async function captureBreakdown(cdp, label, fileName) {
  await clickButtonByText(cdp, label, '#resource-breakdown-panel');
  await waitForCondition(
    cdp,
    `${label} breakdown rows`,
    `(() => {
      const root = document.querySelector('#resource-breakdown-panel');
      if (!root) return false;
      const text = root.textContent ?? '';
      if (text.includes('Loading per-process rankings...')) return false;
      return root.querySelectorAll('tbody tr').length > 0;
    })()`,
    LIVE_WAIT_MS,
  );
  await scrollSelectorIntoView(cdp, '#resource-breakdown-panel');
  await sleep(900);
  await capture(cdp, fileName, false);
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
    if (WARMUP_MS > 0) {
      await sleep(WARMUP_MS);
    }

    await waitForDashboardReady(cdp);
    await sleep(800);
    await capture(cdp, 'dashboard.png');

    await navigateToPage(cdp, 'Metric Trends');
    await waitForMetricTrendsReady(cdp);
    await openResourceBreakdown(cdp);
    await setViewport(cdp, 1920, 1080);
    await captureBreakdown(cdp, 'CPU', 'cpu-trends.png');
    await captureBreakdown(cdp, 'Memory', 'memory-trends.png');
    await captureBreakdown(cdp, 'Network', 'network-trends.png');
    await captureBreakdown(cdp, 'Disk I/O', 'disk-trends.png');

    await setViewport(cdp, 1920, 1400);
    await navigateToPage(cdp, 'GPU Observability');
    await waitForCondition(
      cdp,
      'gpu page ready',
      `(() => {
        const root = document.querySelector('[data-testid="gpu-observability-page"]');
        if (!root) return false;
        const text = root.textContent ?? '';
        return text.includes('GPU Observability')
          && text.includes('GPU Metric Timeline')
          && text.includes('Top GPU Processes')
          && document.querySelectorAll('.recharts-line-curve').length > 0;
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(900);
    await capture(cdp, 'gpu-observability.png');

    await navigateToPage(cdp, 'Security Dashboard');
    await waitForCondition(
      cdp,
      'security dashboard ready',
      `(() => {
        const root = document.querySelector('[data-testid="security-dashboard-page"]');
        if (!root) return false;
        const text = root.textContent ?? '';
        if (text.includes('Loading security findings')) return false;
        return text.includes('Security Findings')
          && text.includes('Security Risk Trend')
          && root.querySelectorAll('section button').length > 0;
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(900);
    await capture(cdp, 'security-dashboard.png');

    await navigateToPage(cdp, 'AGENT');
    await waitForCondition(
      cdp,
      'agent ready',
      `(() => {
        const text = document.body?.innerText ?? '';
        return text.includes('AGENT Query') && text.includes('Run AGENT');
      })()`,
      TIMEOUT_MS,
    );
    await sleep(900);
    await capture(cdp, 'agent-operations.png');

    await navigateToPage(cdp, 'Joint Risk');
    await waitForCondition(
      cdp,
      'joint risk ready',
      `(() => {
        const root = document.querySelector('[data-testid="joint-risk-page"]');
        if (!root) return false;
        const text = root.textContent ?? '';
        if (text.includes('Generating latest joint-risk report...')) return false;
        return text.includes('Ranked Signals')
          && text.includes('Correlation Drilldowns')
          && Array.from(root.querySelectorAll('section'))
            .some((section) => (section.textContent ?? '').includes('Ranked Signals') && section.querySelectorAll('article').length > 0);
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(900);
    await capture(cdp, 'joint-risk.png');

    await navigateToPage(cdp, 'RCA Workflow');
    await waitForCondition(
      cdp,
      'rca ready',
      `(() => {
        const root = document.querySelector('[data-testid="rca-page"]');
        if (!root) return false;
        const text = root.textContent ?? '';
        if (text.includes('Generating latest RCA workflow report...')) return false;
        if (text.includes('No structured report available.')) return false;
        return text.includes('Structured RCA Report')
          && text.includes('Ranked Hypotheses')
          && Array.from(root.querySelectorAll('section'))
            .some((section) => (section.textContent ?? '').includes('Ranked Hypotheses') && section.querySelectorAll('article').length > 0);
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(900);
    await capture(cdp, 'rca.png');

    await navigateToPage(cdp, 'Logs');
    await setViewport(cdp, 1920, 1100);
    await waitForCondition(
      cdp,
      'logs ready',
      `(() => {
        const root = document.querySelector('[data-testid="logs-page"]');
        if (!root) return false;
        const text = root.textContent ?? '';
        if (text.includes('Loading indexed logs…')) return false;
        if (text.includes('No logs match current filters.')) return false;
        return root.querySelectorAll('tbody tr').length > 0;
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(900);
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
