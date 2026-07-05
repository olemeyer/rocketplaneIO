import { test, expect } from '@playwright/test';

// Erwartet laufenden query-Service (ClickHouse) mit Metriken + Login.
test.describe('rocketplaneIO — Metrics-Explorer (M0 signal)', () => {
  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/auth/login', {
      data: { email: 'demo@rocketplane.io', password: 'rocketplane' },
    });
  });

  test('Metrics-Seite listet Metriken und rendert einen Chart', async ({ page }) => {
    await page.goto('/metrics');
    await expect(page.getByRole('heading', { name: 'Metrics' })).toBeVisible();
    // Metrik-Namen in der Liste
    await expect(page.getByText('system.cpu.utilization').first()).toBeVisible();
    // Chart-SVG
    await expect(page.locator('svg[aria-label="metric chart"]')).toBeVisible();
  });

  test('Klick auf eine Metrik lädt deren Zeitreihe', async ({ page }) => {
    await page.goto('/metrics');
    await page.getByRole('button', { name: /http.server.request.count/ }).click();
    await expect(page.getByRole('heading', { name: 'http.server.request.count' })).toBeVisible();
    await expect(page.locator('svg[aria-label="metric chart"]')).toBeVisible();
  });

  test('Screenshot: Metrics', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto('/metrics');
    await expect(page.locator('svg[aria-label="metric chart"]')).toBeVisible();
    await page.waitForTimeout(700);
    await page.screenshot({ path: 'e2e/screenshots/metrics.png' });
  });
});
