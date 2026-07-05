import { test, expect } from '@playwright/test';

// Erwartet laufenden query-Service (ClickHouse) mit Daten + Login.
test.describe('rocketplaneIO — Service Catalog & Detail (M3)', () => {
  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/auth/login', {
      data: { email: 'demo@rocketplane.io', password: 'rocketplane' },
    });
  });

  test('Service-Katalog zeigt Service-Cards', async ({ page }) => {
    await page.goto('/services');
    await expect(page.getByRole('heading', { name: 'Services' })).toBeVisible();
    await expect(page.getByRole('link', { name: /checkout-api/ }).first()).toBeVisible();
    await expect(page.getByRole('link', { name: /payment-gateway/ }).first()).toBeVisible();
  });

  test('Service-Map rendert Knoten und ist klickbar', async ({ page }) => {
    await page.goto('/services');
    await expect(page.getByText('Service map')).toBeVisible();
    // Map-Knoten sind SVG-Gruppen mit role=link.
    const node = page.locator('svg[aria-label="service map"] [role="link"]').first();
    await expect(node).toBeVisible();
    await node.click();
    await expect(page).toHaveURL(/\/services\/[a-z-]+/);
  });

  test('Klick auf einen Service öffnet das Detail mit Charts & Dependencies', async ({ page }) => {
    await page.goto('/services');
    await page.getByRole('link', { name: /checkout-api/ }).first().click();
    await expect(page).toHaveURL(/\/services\/checkout-api/);
    await expect(page.getByRole('heading', { name: 'checkout-api' })).toBeVisible();
    await expect(page.getByText('latency p95')).toBeVisible();
    await expect(page.getByText('Downstream dependencies')).toBeVisible();
    // Deep-Link zu den Traces des Service (href-spezifisch, um Sidebar-Kollision zu vermeiden)
    await expect(page.locator('a[href="/traces?service=checkout-api"]')).toBeVisible();
  });

  test('Screenshot: Service Catalog + Detail', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto('/services');
    await expect(page.getByRole('link', { name: /checkout-api/ }).first()).toBeVisible();
    await page.waitForTimeout(600);
    await page.screenshot({ path: 'e2e/screenshots/service-catalog.png' });

    await page.getByRole('link', { name: /checkout-api/ }).first().click();
    await expect(page.getByText('latency p95')).toBeVisible();
    await page.waitForTimeout(600);
    await page.screenshot({ path: 'e2e/screenshots/service-detail.png' });
  });
});
