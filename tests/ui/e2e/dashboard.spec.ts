import { expect, test } from '@playwright/test';

test.describe('Dashboard Shell', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
  });

  test('loads command center shell', async ({ page }) => {
    await expect(page).toHaveTitle(/AI SRE Command Center/);
    await expect(page.getByRole('heading', { name: /SRE Command Center/i })).toBeVisible();

    await expect(page.locator('button[title="Dashboard"]')).toBeVisible();
    await expect(page.locator('button[title="Metric Trends"]')).toBeVisible();
    await expect(page.locator('button[title="Joint Risk"]')).toBeVisible();
    await expect(page.locator('button[title="RCA Workflow"]')).toBeVisible();
    await expect(page.locator('button[title="GPU Observability"]')).toBeVisible();
    await expect(page.locator('button[title="AGENT"]')).toBeVisible();
    await expect(page.locator('button[title="Data Path Diagnostics"]')).toBeVisible();
  });

  test('core API contracts are stable', async ({ request }) => {
    const healthResp = await request.get('/healthz');
    expect(healthResp.status()).toBe(200);

    const statusResp = await request.get('/api/v1/status');
    expect(statusResp.status()).toBe(200);
    const statusJSON = await statusResp.json();
    expect(statusJSON).toMatchObject({
      version: expect.any(String),
      uptime: expect.any(String),
    });

    const fleetResp = await request.get('/api/v1/fleet');
    expect(fleetResp.status()).toBe(200);
    const fleetJSON = await fleetResp.json();
    expect(Array.isArray(fleetJSON.nodes)).toBeTruthy();
    expect(typeof fleetJSON.count).toBe('number');
  });

  test('initial load has no runtime errors', async ({ page }) => {
    const consoleErrors: string[] = [];
    const runtimeErrors: string[] = [];

    page.on('console', (msg) => {
      if (msg.type() === 'error') {
        consoleErrors.push(msg.text());
      }
    });
    page.on('pageerror', (error) => runtimeErrors.push(String(error)));

    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(800);

    expect(runtimeErrors).toEqual([]);
    expect(consoleErrors).toEqual([]);
  });

  test('renders operations control panel and applies retention update', async ({ page }) => {
    const consoleErrors: string[] = [];
    const requestFailures: string[] = [];

    page.on('console', (msg) => {
      if (msg.type() === 'error') {
        consoleErrors.push(msg.text());
      }
    });
    page.on('response', (response) => {
      const url = response.url();
      if (!url.includes('/api/v1/storage') && !url.includes('/api/v1/ha/status') && !url.includes('/api/v1/finops/signals')) {
        return;
      }
      if (response.status() >= 400) {
        requestFailures.push(`${response.status()} ${url}`);
      }
    });

    const panel = page.getByTestId('operations-control-panel');
    await panel.scrollIntoViewIfNeeded();
    await expect(panel).toBeVisible();
    await expect(panel.getByText(/Retention, HA, and FinOps Control/i)).toBeVisible();

    await panel.getByLabel('Node retention').fill('48h');
    await panel.getByLabel('History samples per node').fill('300');
    await panel.getByRole('button', { name: /Apply retention/i }).click();
    await expect(panel.getByText(/Retention updated|standby mode/i)).toBeVisible();

    expect(requestFailures).toEqual([]);
    expect(consoleErrors).toEqual([]);
  });
});
