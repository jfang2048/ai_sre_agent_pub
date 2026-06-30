#!/usr/bin/env node

import fs from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const ROOT_DIR = path.resolve(SCRIPT_DIR, '..');
const UI_URL = process.env.UI_URL ?? 'http://127.0.0.1:8080';
const OUT_DIR = path.resolve(ROOT_DIR, process.env.SCREENSHOT_DIR ?? 'docs/images');
const TIMEOUT_MS = Number(process.env.CAPTURE_TIMEOUT_MS ?? 30000);
const LIVE_WAIT_MS = Number(process.env.CAPTURE_LIVE_WAIT_MS ?? 45000);
const WARMUP_MS = Number(process.env.CAPTURE_WARMUP_MS ?? 12000);
const STABILIZE_MS = Number(process.env.CAPTURE_STABILIZE_MS ?? 12000);
const MIN_POST_READY_WAIT_MS = 5000;
const VIEWPORT = { width: 1920, height: 1400 };

const ROUTES = [
  {
    name: 'dashboard',
    title: 'Dashboard',
    fileName: 'dashboard.png',
    wait: waitForDashboardReady,
  },
  {
    name: 'risk insights',
    title: 'Risk Insights',
    fileName: 'risk-insights.png',
    wait: waitForRiskInsightsReady,
  },
  {
    name: 'joint risk',
    title: 'Joint Risk',
    fileName: 'joint-risk.png',
    wait: waitForJointRiskReady,
  },
  {
    name: 'rca workflow',
    title: 'RCA Workflow',
    fileName: 'rca.png',
    wait: waitForRCAReady,
  },
];

function resolvePlaywright() {
  const require = createRequire(import.meta.url);
  const searchPaths = [
    path.join(ROOT_DIR, 'tests/ui'),
    path.join(ROOT_DIR, 'frontend'),
    ROOT_DIR,
  ];
  try {
    const resolved = require.resolve('playwright', { paths: searchPaths });
    return require(resolved);
  } catch (error) {
    throw new Error(
      'playwright is not installed for screenshot capture. Run `npm -C tests/ui install` first.',
      { cause: error },
    );
  }
}

function routeStabilizeMs(customMs = STABILIZE_MS) {
  return Math.max(customMs, MIN_POST_READY_WAIT_MS);
}

async function navigateToPage(page, title) {
  const button = page.locator(`button[title="${title}"]`).first();
  await button.waitFor({ state: 'visible', timeout: TIMEOUT_MS });
  await button.click();
}

async function stabilize(page, label, customMs = STABILIZE_MS) {
  const waitMs = routeStabilizeMs(customMs);
  console.log(`stabilizing ${label} for ${waitMs}ms`);
  await page.waitForTimeout(waitMs);
}

async function capture(page, fileName) {
  await page.evaluate(() => window.scrollTo({ top: 0, left: 0, behavior: 'instant' }));
  await page.screenshot({
    path: path.join(OUT_DIR, fileName),
    type: 'png',
    fullPage: false,
  });
}

async function waitForDashboardReady(page) {
  await page.waitForFunction(
    () => {
      const text = document.body?.innerText ?? '';
      if (!text.includes('SRE Command Center')) return false;
      if (text.includes('Awaiting trend samples')) return false;
      const values = Array.from(document.querySelectorAll('div.text-xl.font-semibold'))
        .map((el) => (el.textContent ?? '').replace(/[^0-9.+-]/g, ''))
        .map((raw) => Number.parseFloat(raw))
        .filter((value) => Number.isFinite(value));
      return values.filter((value) => value > 0.05).length >= 2;
    },
    { timeout: LIVE_WAIT_MS },
  );
}

async function waitForRiskInsightsReady(page) {
  await page.waitForSelector('[data-testid="risk-insights-page"]', { timeout: TIMEOUT_MS });
  await page.waitForFunction(
    () => {
      const root = document.querySelector('[data-testid="risk-insights-page"]');
      if (!root) return false;
      const text = root.textContent ?? '';
      if (text.includes('Generating latest risk findings...')) return false;
      if (text.includes('Select a risk finding to inspect evidence.')) return false;
      return text.includes('Ranked Potential Risks') &&
        text.includes('Evidence Breakdown') &&
        text.includes('Weak-Signal and Event Summary') &&
        Array.from(root.querySelectorAll('section')).some(
          (section) =>
            (section.textContent ?? '').includes('Ranked Potential Risks') &&
            section.querySelectorAll('button').length > 0,
        );
    },
    { timeout: LIVE_WAIT_MS },
  );
}

async function waitForJointRiskReady(page) {
  await page.waitForSelector('[data-testid="joint-risk-page"]', { timeout: TIMEOUT_MS });
  await page.waitForFunction(
    () => {
      const root = document.querySelector('[data-testid="joint-risk-page"]');
      if (!root) return false;
      const text = root.textContent ?? '';
      if (text.includes('Generating latest joint-risk report...')) return false;
      return text.includes('Ranked Signals') &&
        text.includes('Correlation Drilldowns') &&
        Array.from(root.querySelectorAll('section')).some(
          (section) =>
            (section.textContent ?? '').includes('Ranked Signals') &&
            section.querySelectorAll('article').length > 0,
        );
    },
    { timeout: LIVE_WAIT_MS },
  );
}

async function waitForRCAReady(page) {
  await page.waitForSelector('[data-testid="rca-page"]', { timeout: TIMEOUT_MS });
  await page.waitForFunction(
    () => {
      const root = document.querySelector('[data-testid="rca-page"]');
      if (!root) return false;
      const text = root.textContent ?? '';
      if (text.includes('Generating latest RCA workflow report...')) return false;
      if (text.includes('No structured report available.')) return false;
      return text.includes('Structured RCA Report') &&
        text.includes('Ranked Hypotheses') &&
        Array.from(root.querySelectorAll('section')).some(
          (section) =>
            (section.textContent ?? '').includes('Ranked Hypotheses') &&
            section.querySelectorAll('article').length > 0,
        );
    },
    { timeout: LIVE_WAIT_MS },
  );
}

async function main() {
  const { chromium } = resolvePlaywright();
  await fs.mkdir(OUT_DIR, { recursive: true });

  const browser = await chromium.launch({
    headless: true,
    args: ['--disable-gpu', '--no-sandbox'],
  });
  const page = await browser.newPage({ viewport: VIEWPORT });

  try {
    await page.goto(UI_URL, { waitUntil: 'domcontentloaded', timeout: TIMEOUT_MS });
    await page.waitForSelector('main', { timeout: TIMEOUT_MS });
    await page.waitForSelector('button[title="Dashboard"]', { timeout: TIMEOUT_MS });
    if (WARMUP_MS > 0) {
      console.log(`warming up for ${WARMUP_MS}ms`);
      await page.waitForTimeout(WARMUP_MS);
    }

    for (const route of ROUTES) {
      await navigateToPage(page, route.title);
      await route.wait(page);
      await stabilize(page, route.name);
      await capture(page, route.fileName);
      console.log(`captured ${route.fileName}`);
    }
  } finally {
    await page.close();
    await browser.close();
  }

  console.log(`Saved screenshots to ${OUT_DIR}`);
}

main().catch((error) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
});
