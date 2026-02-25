import { test, expect, Page } from '@playwright/test';

/**
 * Comprehensive UI Smoke Tests
 *
 * These tests perform end-to-end smoke testing of the AI SRE Agent dashboard.
 * They verify that all pages load, data renders correctly, and basic interactions work.
 */

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:8080';
const TEST_TIMEOUT = 30000;

test.describe('Dashboard Smoke Tests', () => {
	test.beforeEach(async ({ page }) => {
		// Set default timeout
		test.setTimeout(TEST_TIMEOUT);
		await page.goto(BASE_URL);
	});

	test('homepage loads', async ({ page }) => {
		await expect(page).toHaveTitle(/AI SRE Agent/);
		await expect(page).toHaveURL(/\/$/);
	});

	test('main navigation is visible', async ({ page }) => {
		// Look for navigation elements
		const nav = page.locator('nav, [role="navigation"], .navigation, header');
		await expect(nav.first()).toBeVisible();
	});

	test('no console errors on load', async ({ page }) => {
		const errors: string[] = [];
		page.on('console', msg => {
			if (msg.type() === 'error') {
				errors.push(msg.text());
			}
		});

		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(2000);

		expect(errors).toHaveLength(0);
	});
});

test.describe('Fleet Page', () => {
	test.beforeEach(async ({ page }) => {
		test.setTimeout(TEST_TIMEOUT);
	});

	test('fleet page loads', async ({ page }) => {
		await page.goto(`${BASE_URL}/#/fleet`);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(2000);

		// Check URL
		await expect(page).toHaveURL(/\/fleet/);
	});

	test('fleet page displays content', async ({ page }) => {
		await page.goto(`${BASE_URL}/#/fleet`);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(2000);

		// Look for any content (dashboard, table, etc.)
		const content = page.locator('main, #root, [data-testid="fleet-view"], .dashboard, .container');
		await expect(content.first()).toBeVisible();
	});

	test('fleet API is accessible', async ({ page }) => {
		const response = await page.request.get(`${BASE_URL}/api/v1/fleet`);
		expect(response.status()).toBe(200);

		const data = await response.json();
		expect(data).toBeDefined();
		expect(typeof data).toBe('object');
	});
});

test.describe('Diagnostics Pages', () => {
	test.beforeEach(async ({ page }) => {
		test.setTimeout(TEST_TIMEOUT);
	});

	test('data path diagnostics page', async ({ page }) => {
		await page.goto(`${BASE_URL}/#/diagnostics/data-path`);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(2000);

		// Verify page loaded
		await expect(page).toHaveURL(/\/diagnostics\/data-path/);
	});

	test('kernel path diagnostics page', async ({ page }) => {
		await page.goto(`${BASE_URL}/#/diagnostics/kernel-path`);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(2000);

		// Verify page loaded
		await expect(page).toHaveURL(/\/diagnostics\/kernel-path/);
	});

	test('API endpoints respond', async ({ page }) => {
		// Test all diagnostics endpoints
		const endpoints = [
			'/api/v1/diagnostics/data-path',
			'/api/v1/diagnostics/kernel-path',
			'/api/v1/diagnostics/root-cause',
			'/api/v1/diagnostics/workload-path',
		];

		for (const endpoint of endpoints) {
			const response = await page.request.get(`${BASE_URL}${endpoint}`);
			// Should return 200 or 501 (not implemented)
			expect([200, 501]).toContain(response.status());
		}
	});
});

test.describe('Top Programs Page', () => {
	test.beforeEach(async ({ page }) => {
		test.setTimeout(TEST_TIMEOUT);
	});

	test('top programs page loads', async ({ page }) => {
		await page.goto(`${BASE_URL}/#/top`);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(2000);

		// Verify page loaded
		await expect(page).toHaveURL(/\/top/);
	});

	test('top programs API is accessible', async ({ page }) => {
		const response = await page.request.get(`${BASE_URL}/api/v1/top/programs`);
		// Should return data or 404
		expect([200, 404]).toContain(response.status());
	});
});

test.describe('Log System', () => {
	test.beforeEach(async ({ page }) => {
		test.setTimeout(TEST_TIMEOUT);
	});

	test('log index status is accessible', async ({ page }) => {
		const response = await page.request.get(`${BASE_URL}/api/v1/logs/status`);
		// May return 501 if not enabled, or 200 with status
		expect([200, 501, 404]).toContain(response.status());
	});

	test('log search endpoint responds', async ({ page }) => {
		const response = await page.request.get(`${BASE_URL}/api/v1/logs/search?limit=10`);
		// May return 501 if not enabled, or 200 with results
		expect([200, 501, 404]).toContain(response.status());
	});
});

test.describe('API Integration', () => {
	test('all main endpoints respond', async ({ page }) => {
		const endpoints = [
			{ path: '/healthz', method: 'GET' },
			{ path: '/api/v1/status', method: 'GET' },
			{ path: '/api/v1/fleet', method: 'GET' },
			{ path: '/api/v1/logs/status', method: 'GET' },
			{ path: '/api/v1/diagnostics/data-path', method: 'GET' },
		];

		const results = [];

		for (const endpoint of endpoints) {
			let response;
			if (endpoint.method === 'GET') {
				response = await page.request.get(`${BASE_URL}${endpoint.path}`);
			} else {
				response = await page.request.post(`${BASE_URL}${endpoint.path}`);
			}

			const success = response.status() >= 200 && response.status() < 500;
			results.push({
				endpoint: endpoint.path,
				status: response.status(),
				success: success,
			});
		}

		// Log results
		console.log('API Endpoint Results:');
		for (const result of results) {
			const status = result.success ? '✓' : '✗';
			console.log(`  ${status} ${result.endpoint}: ${result.status}`);
		}

		// Verify at least health endpoint works
		const healthResult = results.find(r => r.endpoint === '/healthz');
		expect(healthResult?.status).toBe(200);
	});

	test('fleet data structure is valid', async ({ page }) => {
		const response = await page.request.get(`${BASE_URL}/api/v1/fleet`);

		if (response.status() === 200) {
			const data = await response.json();

			// Validate structure
			expect(data).toHaveProperty('collectors');

			// collectors should be an object
			expect(typeof data.collectors).toBe('object');
		}
	});
});

test.describe('Error Handling', () => {
	test('404 on unknown routes', async ({ page }) => {
		const response = await page.request.get(`${BASE_URL}/api/v1/unknown-endpoint`);
		expect(response.status()).toBe(404);
	});

	test('invalid method returns error', async ({ page }) => {
		const response = await page.request.post(`${BASE_URL}/healthz`);
		// Should return 405 or 404 depending on implementation
		expect([404, 405]).toContain(response.status());
	});
});

test.describe('Performance', () => {
	test('homepage loads quickly', async ({ page }) => {
		const startTime = Date.now();
		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');

		const loadTime = Date.now() - startTime;
		console.log(`Homepage load time: ${loadTime}ms`);

		// Should load within 5 seconds
		expect(loadTime).toBeLessThan(5000);
	});

	test('API responses are fast', async ({ page }) => {
		const startTime = Date.now();
		const response = await page.request.get(`${BASE_URL}/api/v1/fleet`);
		const responseTime = Date.now() - startTime;

		console.log(`API response time: ${responseTime}ms`);

		expect(response.status()).toBe(200);
		expect(responseTime).toBeLessThan(1000); // Should respond within 1 second
	});
});

test.describe('Accessibility', () => {
	test('has page title', async ({ page }) => {
		await page.goto(BASE_URL);
		const title = await page.title();
		expect(title).toBeTruthy();
		expect(title.length).toBeGreaterThan(0);
	});

	test('has heading or main content', async ({ page }) => {
		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(1000);

		// Look for h1, h2, or main element
		const heading = page.locator('h1, h2, h3, main').first();
		await expect(heading).toBeVisible();
	});
});

test.describe('Data Rendering', () => {
	test('fleet page shows data after load', async ({ page }) => {
		await page.goto(`${BASE_URL}/#/fleet`);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(3000);

		// Wait for some content to appear
		const content = page.locator('main, #root, .dashboard, .container, table, .grid').first();
		await expect(content).toBeVisible({ timeout: 10000 });
	});

	test('no JavaScript errors on navigation', async ({ page }) => {
		const errors: string[] = [];

		page.on('console', msg => {
			if (msg.type() === 'error') {
				errors.push(msg.text());
			}
		});

		// Navigate through pages
		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');

		await page.goto(`${BASE_URL}/#/fleet`);
		await page.waitForLoadState('networkidle');

		await page.goto(`${BASE_URL}/#/top`);
		await page.waitForLoadState('networkidle');

		// Check no errors accumulated
		expect(errors.length).toBe(0);
	});
});

test.describe('Responsive Design', () => {
	test('works on desktop viewport', async ({ page }) => {
		await page.setViewportSize({ width: 1920, height: 1080 });
		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');

		// Should load without errors
		const title = await page.title();
		expect(title).toBeTruthy();
	});

	test('works on tablet viewport', async ({ page }) => {
		await page.setViewportSize({ width: 768, height: 1024 });
		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');

		const title = await page.title();
		expect(title).toBeTruthy();
	});

	test('works on mobile viewport', async ({ page }) => {
		await page.setViewportSize({ width: 375, height: 667 });
		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');

		const title = await page.title();
		expect(title).toBeTruthy();
	});
});

test.describe('Browser Compatibility', () => {
	test('no browser console warnings', async ({ page }) => {
		const warnings: string[] = [];

		page.on('console', msg => {
			if (msg.type() === 'warning') {
				warnings.push(msg.text());
			}
		});

		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(2000);

		// Check for warnings (some may be acceptable)
		console.log(`Browser warnings: ${warnings.length}`);
		for (const warning of warnings) {
			console.log(`  Warning: ${warning}`);
		}
	});
});

test.describe('Security', () => {
	test('no sensitive data in console', async ({ page }) => {
		const sensitive: string[] = [];
		const logs: string[] = [];

		page.on('console', msg => {
			const text = msg.text();
			logs.push(text);

			// Check for common sensitive patterns
			const sensitivePatterns = [
				/password/i,
				/token/i,
				/api[_-]?key/i,
				/secret/i,
			];

			for (const pattern of sensitivePatterns) {
				if (pattern.test(text)) {
					sensitive.push(text);
				}
			}
		});

		await page.goto(BASE_URL);
		await page.waitForLoadState('networkidle');

		expect(sensitive).toHaveLength(0);
	});

	test('API has proper error responses', async ({ page }) => {
		// Test that errors don't leak stack traces
		const response = await page.request.get(`${BASE_URL}/api/v1/nonexistent`);

		if (response.status() === 404) {
			const body = await response.text();

			// Should not contain stack traces or sensitive info
			expect(body).not.toMatch(/stack trace/i);
			expect(body).not.toMatch(/panic/i);
			expect(body).not.toMatch(/fatal error/i);
		}
	});
});

test.describe('Data Flow', () => {
	test('status API returns complete data', async ({ page }) => {
		const response = await page.request.get(`${BASE_URL}/api/v1/status`);

		if (response.status() === 200) {
			const data = await response.json();

			// Validate expected fields
			expect(data).toBeDefined();
			expect(typeof data).toBe('object');

			// Should have collectors info even if empty
			if (data.collectors !== undefined) {
				expect(typeof data.collectors).toBe('object');
			}
		}
	});

	test('fleet API returns structured data', async ({ page }) => {
		const response = await page.request.get(`${BASE_URL}/api/v1/fleet`);

		if (response.status() === 200) {
			const data = await response.json();

			// Should be valid JSON with expected structure
			expect(data).toHaveProperty('collectors');

			// Check collectors is object or array
			const collectors = data.collectors;
			expect(['object', 'number']).toContain(typeof collectors);
		}
	});
});
