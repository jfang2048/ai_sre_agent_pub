import { expect, test } from '@playwright/test';

test.describe('Knowledge Page', () => {
  test('queries the local knowledge base and renders source-linked hits without runtime or request failures', async ({ page }) => {
    const consoleErrors: string[] = [];
    const runtimeErrors: string[] = [];
    const requestFailures: string[] = [];

    page.on('console', (msg) => {
      if (msg.type() === 'error') {
        consoleErrors.push(msg.text());
      }
    });
    page.on('pageerror', (error) => runtimeErrors.push(String(error)));
    page.on('requestfailed', (request) => requestFailures.push(`${request.failure()?.errorText} ${request.url()}`));

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    await page.getByTitle('Knowledge Base').click();
    await page.waitForLoadState('networkidle');

    await expect(page.getByRole('heading', { level: 1, name: /Knowledge Base/i })).toBeVisible();
    await expect(page.getByTestId('knowledge-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Retrieved Snippets' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Document Detail' })).toBeVisible();

    const queryInput = page.getByPlaceholder(/Search for incidents, runbooks, deployment errors/i);
    await queryInput.fill('timeout deployment runbook cache');
    await page.getByRole('button', { name: /^Query$/ }).click();

    await expect.poll(async () =>
      page.locator('[data-testid="knowledge-page"] section:has-text("Retrieved Snippets") button').count(),
    ).toBeGreaterThan(0);
    await expect(page.locator('[data-testid="knowledge-page"] section:has-text("Retrieved Snippets")')).toContainText(/score=/i);
    await expect(page.locator('[data-testid="knowledge-page"] section:has-text("Document Detail")')).toContainText(/chunk=/i);

    expect(runtimeErrors).toEqual([]);
    expect(consoleErrors).toEqual([]);
    expect(requestFailures).toEqual([]);
  });
});
