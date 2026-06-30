#!/usr/bin/env node

import fs from 'node:fs/promises';
import path from 'node:path';

const DEVTOOLS_PORT = Number(process.env.CHROME_DEVTOOLS_PORT ?? 9223);
const UI_URL = process.env.UI_URL ?? 'http://127.0.0.1:8080/ui';
const OUT_DIR = process.env.SCREENSHOT_DIR ?? 'docs/images/ui-captures/generated';
const TIMEOUT_MS = Number(process.env.CAPTURE_TIMEOUT_MS ?? 30000);
const LIVE_WAIT_MS = Number(process.env.CAPTURE_LIVE_WAIT_MS ?? 45000);
const WARMUP_MS = Number(process.env.CAPTURE_WARMUP_MS ?? 20000);
const CAPTURE_STRICT = parseBoolEnv(process.env.CAPTURE_STRICT);

const BREAKDOWN_CAPTURE_META = {
  cpu: { label: 'CPU', fileName: 'screenshot_ui_cpu.png', key: 'trends_cpu' },
  memory: { label: 'Memory', fileName: 'screenshot_ui_memory.png', key: 'trends_memory' },
  nic: { label: 'Network', fileName: 'screenshot_ui_nic.png', key: 'trends_nic' },
  disk: { label: 'Disk I/O', fileName: 'screenshot_ui_disk.png', key: 'trends_disk' },
};

const TOP_LEVEL_CAPTURE_KEYS = [
  'dashboard_live',
  'trends_live',
  'trends_breakdown',
  'dashboard_full',
  'data_path_diagnostics',
  'data_path_model',
  'gpu_observability',
  'agent',
];
const BREAKDOWN_ALIASES = {
  network: 'nic',
  disk_io: 'disk',
};
const BREAKDOWN_VARIANT_KEYS = Object.keys(BREAKDOWN_CAPTURE_META);
const BREAKDOWN_VARIANT_SET = new Set(BREAKDOWN_VARIANT_KEYS);
const CAPTURE_KEYS = [
  ...TOP_LEVEL_CAPTURE_KEYS,
  ...Object.values(BREAKDOWN_CAPTURE_META).map((meta) => meta.key),
];
const CAPTURE_KEY_SET = new Set(CAPTURE_KEYS);

const CAPTURE_ONLY = parseCaptureOnly(process.env.CAPTURE_ONLY);
const BREAKDOWN_VARIANTS_RAW = parseCsvSet(process.env.CAPTURE_BREAKDOWN_VARIANTS);
const BREAKDOWN_VARIANTS = parseBreakdownVariants(BREAKDOWN_VARIANTS_RAW);
const CLI_ARGS = new Set(process.argv.slice(2));

handleCliFlags(CLI_ARGS);
validateCaptureConfig();
if (CLI_ARGS.has('--print-plan')) {
  printPlan();
  process.exit(0);
}

function parseCsvSet(value) {
  if (!value || !value.trim()) {
    return new Set();
  }
  return new Set(
    value
      .split(',')
      .map((part) => part.trim().toLowerCase())
      .filter(Boolean),
  );
}

function parseCaptureOnly(value) {
  return parseCsvSet(value);
}

function parseBreakdownVariants(requested) {
  if (!requested.size) {
    return ['cpu', 'memory', 'nic', 'disk'];
  }

  const normalized = [];
  for (const item of requested) {
    const alias = BREAKDOWN_ALIASES[item];
    const candidate = alias || item;
    if (candidate in BREAKDOWN_CAPTURE_META) {
      normalized.push(candidate);
    }
  }
  return normalized.length ? normalized : ['cpu', 'memory', 'nic', 'disk'];
}

function parseBoolEnv(value) {
  if (!value) {
    return false;
  }
  return value.trim() === '1' || value.trim().toLowerCase() === 'true';
}

function validateCaptureConfig() {
  const unknownCaptureKeys = [...CAPTURE_ONLY].filter((key) => !CAPTURE_KEY_SET.has(key));
  if (unknownCaptureKeys.length > 0) {
    const message = `Unknown CAPTURE_ONLY keys: ${unknownCaptureKeys.join(', ')}. Allowed: ${CAPTURE_KEYS.join(', ')}`;
    if (CAPTURE_STRICT) {
      exitStrictConfigError(message);
    }
    console.warn(message);
  }

  const unknownBreakdownVariants = [...BREAKDOWN_VARIANTS_RAW].filter((item) => {
    const candidate = BREAKDOWN_ALIASES[item] || item;
    return !BREAKDOWN_VARIANT_SET.has(candidate);
  });
  if (unknownBreakdownVariants.length > 0) {
    const aliases = Object.entries(BREAKDOWN_ALIASES)
      .map(([source, target]) => `${source}=${target}`)
      .join(', ');
    const message = `Unknown CAPTURE_BREAKDOWN_VARIANTS values: ${unknownBreakdownVariants.join(', ')}. Allowed: ${BREAKDOWN_VARIANT_KEYS.join(', ')}. Aliases: ${aliases}`;
    if (CAPTURE_STRICT) {
      exitStrictConfigError(message);
    }
    console.warn(message);
  }

  const requestedBreakdownKeys = [...CAPTURE_ONLY].filter((key) =>
    Object.values(BREAKDOWN_CAPTURE_META).some((meta) => meta.key === key),
  );
  if (requestedBreakdownKeys.length > 0) {
    const enabledBreakdownKeys = new Set(
      BREAKDOWN_VARIANTS
        .map((variant) => BREAKDOWN_CAPTURE_META[variant]?.key)
        .filter(Boolean),
    );
    const disabledRequested = requestedBreakdownKeys.filter((key) => !enabledBreakdownKeys.has(key));
    if (disabledRequested.length > 0) {
      const message = `CAPTURE_ONLY requests breakdown keys not enabled by CAPTURE_BREAKDOWN_VARIANTS: ${disabledRequested.join(', ')}. Enabled breakdown keys: ${[...enabledBreakdownKeys].join(', ')}`;
      if (CAPTURE_STRICT) {
        exitStrictConfigError(message);
      }
      console.warn(message);
    }
  }
}

function exitStrictConfigError(message) {
  process.stderr.write(`${message}\n`);
  process.exit(2);
}

function printCaptureKeys() {
  console.log(`Capture keys: ${CAPTURE_KEYS.join(', ')}`);
  const aliases = Object.entries(BREAKDOWN_ALIASES)
    .map(([source, target]) => `${source}=${target}`)
    .join(', ');
  console.log(`Breakdown variants: ${BREAKDOWN_VARIANT_KEYS.join(', ')} (aliases: ${aliases})`);
}

function printHelp() {
  console.log('Usage: node scripts/capture_ui_screenshots.mjs [--help] [--list-capture-keys] [--print-plan]');
  console.log('Environment: CAPTURE_ONLY, CAPTURE_BREAKDOWN_VARIANTS, CAPTURE_STRICT=1, CAPTURE_WARMUP_MS, CAPTURE_LIVE_WAIT_MS, CHROME_DEVTOOLS_PORT, UI_URL, SCREENSHOT_DIR');
}

function selectedCapturePlan() {
  const selected = [];
  for (const key of TOP_LEVEL_CAPTURE_KEYS) {
    if (shouldCapture(key)) {
      selected.push(key);
    }
  }
  for (const variant of BREAKDOWN_VARIANTS) {
    const meta = BREAKDOWN_CAPTURE_META[variant];
    if (meta?.key && shouldCapture(meta.key)) {
      selected.push(meta.key);
    }
  }
  return selected;
}

function printPlan() {
  const selected = selectedCapturePlan();
  console.log(`Selected captures (${selected.length}): ${selected.join(', ') || '(none)'}`);
  console.log(`CAPTURE_ONLY=${process.env.CAPTURE_ONLY ?? ''}`);
  console.log(`CAPTURE_BREAKDOWN_VARIANTS=${process.env.CAPTURE_BREAKDOWN_VARIANTS ?? ''}`);
  console.log(`CAPTURE_STRICT=${CAPTURE_STRICT ? '1' : '0'}`);
}

function handleCliFlags(args) {
  if (args.has('--help') || args.has('-h')) {
    printHelp();
    process.exit(0);
  }
  if (args.has('--list-capture-keys')) {
    printCaptureKeys();
    process.exit(0);
  }
}

function shouldCapture(key) {
  return CAPTURE_ONLY.size === 0 || CAPTURE_ONLY.has(key);
}

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

async function clickButtonByText(cdp, text, scopeSelector = '') {
  const escapedText = JSON.stringify(text);
  const escapedScope = JSON.stringify(scopeSelector);
  const ok = await evalExpr(
    cdp,
    `(() => {
      const scope = ${escapedScope};
      const root = scope ? document.querySelector(scope) : document;
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

async function captureShot(cdp, key, fileName, opts = {}) {
  if (!shouldCapture(key)) {
    return;
  }
  await saveShot(cdp, path.join(OUT_DIR, fileName), opts);
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
    if (WARMUP_MS > 0) {
      await sleep(WARMUP_MS);
    }

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
    await captureShot(cdp, 'dashboard_live', 'screenshot_ui_dashboard_live.png');
    if (shouldCapture('dashboard_live')) {
      // Keep legacy README path stable.
      await fs.copyFile(dashboardLive, path.join(OUT_DIR, 'screenshot_ui_web.png'));
      await fs.copyFile(dashboardLive, path.join(OUT_DIR, 'screenshot_ui_root.png'));
    }

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
    await captureShot(cdp, 'trends_live', 'screenshot_ui_trends_live.png');

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
    await captureShot(cdp, 'trends_breakdown', 'screenshot_ui_trends_breakdown.png');

    for (const variant of BREAKDOWN_VARIANTS) {
      const meta = BREAKDOWN_CAPTURE_META[variant];
      if (!meta || !shouldCapture(meta.key)) {
        continue;
      }
      await clickButtonByText(cdp, meta.label, '#resource-breakdown-panel');
      await sleep(900);
      await captureShot(cdp, meta.key, meta.fileName, { captureBeyondViewport: false });
    }

    await clickSelector(cdp, 'button[title=\"Dashboard\"]');
    await sleep(1500);
    await setViewport(cdp, 1920, 1800);
    await captureShot(cdp, 'dashboard_full', 'screenshot_ui_dashboard_full.png');

    await clickSelector(cdp, 'button[title=\"Data Path Diagnostics\"]');
    await waitForCondition(
      cdp,
      'data path diagnostics ready',
      `(() => {
        const text = document.body?.innerText ?? '';
        if (!text.includes('Data Path Diagnostics')) return false;
        if (text.includes('Loading network diagnostics...')) return false;
        if (text.includes('Loading storage diagnostics...')) return false;
        return true;
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(1200);
    await captureShot(cdp, 'data_path_diagnostics', 'screenshot_ui_data_path_diagnostics.png');

    await setViewport(cdp, 1920, 900);
    await evalExpr(
      cdp,
      `(() => {
        const target = Array.from(document.querySelectorAll('section'))
          .find((node) => (node.textContent ?? '').includes('Unified Data Path Model'));
        if (target) target.scrollIntoView({ behavior: 'instant', block: 'start' });
        return Boolean(target);
      })()`,
    );
    await sleep(1200);
    await captureShot(cdp, 'data_path_model', 'screenshot_ui_data_path_model.png', {
      captureBeyondViewport: false,
    });

    await clickSelector(cdp, 'button[title=\"GPU Observability\"]');
    await waitForCondition(
      cdp,
      'gpu observability ready',
      `(() => {
        const text = document.body?.innerText ?? '';
        if (!text.includes('GPU Observability')) return false;
        if (!text.includes('GPU Metric Timeline')) return false;
        return true;
      })()`,
      LIVE_WAIT_MS,
    );
    await sleep(1000);
    await captureShot(cdp, 'gpu_observability', 'screenshot_ui_gpu_observability.png');

    await clickSelector(cdp, 'button[title=\"AGENT\"]');
    await waitForCondition(
      cdp,
      'agent page ready',
      `(() => {
        const text = document.body?.innerText ?? '';
        return text.includes('AGENT Query') && text.includes('Run AGENT');
      })()`,
      TIMEOUT_MS,
    );
    await sleep(900);
    await captureShot(cdp, 'agent', 'screenshot_ui_agent.png');
  } finally {
    await cdp.close();
    await closePage(DEVTOOLS_PORT, page.id);
  }
}

main().catch((err) => {
  console.error(err instanceof Error ? err.message : String(err));
  process.exitCode = 1;
});
