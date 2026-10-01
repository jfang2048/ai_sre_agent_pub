import { expect, test, type Page } from '@playwright/test';

test.use({ timezoneId: 'UTC', video: 'off' });
const observed = '2026-10-01T12:00:00Z';
const reading = (value: number, unit: string) => ({ value, unit, observed_at: observed });
const finding = {
  code: 'request_backlog', severity: 'warning', summary: 'Requests are waiting for serving capacity.',
  recommendation: 'Compare running requests, cache occupancy and same-node GPU evidence before changing capacity.',
  evidence: [`requests_waiting = 18 requests at ${observed}`],
};

// Synthetic fixtures only. These tests never require a GPU or external model.
async function fixture(page: Page, theme: 'light' | 'dark', failFirst = false) {
  await page.addInitScript(theme => {
    localStorage.setItem('dashboard-store', JSON.stringify({ version: 4, state: { theme } }));
  }, theme);
  let calls = 0;
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname;
    let body: unknown = {};
    if (path === '/api/v1/inference/overview') {
      if (failFirst && calls++ === 0) {
        await route.fulfill({ status: 503, contentType: 'application/json', body: '{}' });
        return;
      }
      body = {
        generated_at: observed, stale_after_seconds: 120, capacity_limited: false,
        endpoints: [{ collector_id: 'synthetic-a', hostname: 'serving-node', endpoint: 'local-serving',
          status: 'fresh', last_attempt_at: observed, last_success_at: observed,
          models: [{ name: 'example-model', engine: '0', observed_at: observed, status: 'fresh',
            metrics: { requests_waiting: reading(18, 'requests'), requests_running: reading(0, 'requests'),
              kv_cache_utilization_percent: reading(94, 'percent'),
              ttft_mean_seconds: { ...reading(2.4, 'seconds'), window_seconds: 30 },
              generation_tokens_per_second: { ...reading(35, 'tokens/s'), window_seconds: 30 } },
            findings: [finding] },
          { name: 'example-model', engine: '1', observed_at: observed, status: 'stale', metrics: {}, findings: [] }] },
        { collector_id: 'synthetic-b', hostname: 'unreachable-node', endpoint: 'other-serving',
          status: 'unavailable', last_attempt_at: observed, models: [] }],
        gpus: [{ collector_id: 'synthetic-a', hostname: 'serving-node', gpu_id: '0', observed_at: observed,
          status: 'fresh', metrics: { memory_used_percent: reading(96, 'percent'),
            utilization_sm_percent: reading(80, 'percent'), temperature_celsius: reading(72, 'celsius') }, findings: [] }],
      };
    } else if (path === '/api/v1/gpu/nodes' || path === '/api/v1/fleet') {
      body = { nodes: [], count: 0, timestamp: observed };
    } else if (path === '/api/v1/status') {
      body = { version: 'v0.95', uptime: '30m', total_nodes: 0, healthy_nodes: 0 };
    } else {
      await route.fulfill({ status: 503, contentType: 'application/json', body: '{}' });
      return;
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
  });
}

for (const theme of ['light', 'dark'] as const) {
  test(`serving evidence renders with zero, missing values and keyboard disclosure in ${theme}`, async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    await fixture(page, theme);
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.goto('/?page=gpu-observability');
    const panel = page.getByTestId('inference-monitoring-panel');
    await expect(panel.getByText('2.4 s', { exact: true })).toBeVisible();
    await expect(panel.getByText('0 requests', { exact: true })).toBeVisible();
    await expect(panel.getByText('Unavailable', { exact: true }).first()).toBeVisible();
    await expect(panel.getByRole('meter', { name: 'KV-cache occupancy' })).toHaveAttribute('value', '94');
    const disclosure = panel.locator('summary').first();
    await disclosure.focus();
    await page.keyboard.press('Enter');
    await expect(panel.getByText(finding.evidence[0], { exact: true })).toBeVisible();
    await panel.screenshot({ path: `test-results/screenshots/inference-${theme}.png` });
    await page.setViewportSize({ width: 360, height: 740 });
    await panel.scrollIntoViewIfNeeded();
    expect(await panel.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    expect(await page.locator('main').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await panel.getByRole('heading', { name: 'LLM serving & GPU evidence' }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: `test-results/screenshots/inference-${theme}-mobile.png` });
    await panel.getByRole('heading', { name: 'GPU 0 · Recent observations' }).scrollIntoViewIfNeeded();
    await expect(panel.getByText('96 %', { exact: true })).toBeVisible();
    await page.screenshot({ path: `test-results/screenshots/inference-${theme}-mobile-gpu.png` });
    await page.getByRole('combobox', { name: 'Model and engine' }).selectOption(JSON.stringify(['example-model', '1']));
    await expect(panel.getByText('This model has stale observations even if the target is reachable.')).toBeVisible();
    await expect(panel.getByText(finding.summary)).toHaveCount(0);
    await page.getByRole('combobox', { name: 'Serving target' }).selectOption(JSON.stringify(['synthetic-b', 'other-serving']));
    await expect(panel.getByText('No model measurements available from this target.')).toBeVisible();
    await expect(panel.getByText('No GPU observations in this scope.')).toBeVisible();
    expect(errors).toEqual([]);
  });
}

test('failed overview has an actionable retry', async ({ page }) => {
  await fixture(page, 'light', true);
  await page.goto('/?page=gpu-observability');
  const panel = page.getByTestId('inference-monitoring-panel');
  await expect(panel.getByRole('alert')).toContainText('Unable to load monitoring evidence');
  await panel.getByRole('button', { name: 'Retry monitoring' }).click();
  await expect(panel.getByText('2.4 s', { exact: true })).toBeVisible();
});
