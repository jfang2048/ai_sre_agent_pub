import { expect, test } from '@playwright/test';

test.describe('Visual Smoke', () => {
  test('dashboard screenshot', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('main').first()).toBeVisible();
    await capture(page, 'dashboard');
  });

  test('metric trends screenshot', async ({ page }) => {
    await page.goto('/');
    await page.getByTitle('Metric Trends').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { name: /Metric Trends/i })).toBeVisible();
    await capture(page, 'metric-trends');
  });

  test('data path diagnostics screenshot', async ({ page }) => {
    await page.goto('/');
    await page.getByTitle('Data Path Diagnostics').click();
    await page.waitForLoadState('networkidle');
    await expect(page.getByRole('heading', { name: /Data Path Diagnostics/i })).toBeVisible();
    await capture(page, 'data-path-diagnostics');
  });
});

async function capture(page: any, name: string) {
  await page.screenshot({
    path: `test-results/screenshots/${name}.png`,
    fullPage: true,
  });
}
