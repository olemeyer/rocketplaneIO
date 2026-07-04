import { test, expect } from '@playwright/test';

// Diese Specs erwarten einen laufenden query-Service (seed) auf :7080, den der
// Next-Dev-Server via /api/rp-Proxy anspricht. Lokal:
//   QUERY_STORE=seed query &  +  next dev  ->  pnpm exec playwright test explore
test.describe('rocketplane — /explore (live gegen query-Service)', () => {
  test.beforeEach(async ({ page }) => {
    // /explore ist geschützt -> zuerst per Demo-Login eine Session setzen.
    await page.request.post('/api/auth/login', {
      data: { email: 'demo@rocketplane.io', password: 'rocketplane' },
    });
    await page.goto('/explore');
  });

  test('lädt Service-Health live', async ({ page }) => {
    // exact:true, sonst kollidiert z.B. "inventory" mit dem Span "inventory.reserve".
    await expect(page.getByText('payment-gateway', { exact: true })).toBeVisible();
    await expect(page.getByText('cart-service', { exact: true })).toBeVisible();
    await expect(page.getByText('inventory', { exact: true })).toBeVisible();
  });

  test('rendert den Trace-Waterfall mit echten Spans', async ({ page }) => {
    await expect(page.getByText('POST /checkout', { exact: true })).toBeVisible();
    await expect(page.getByText('payment.charge', { exact: true })).toBeVisible();
    await expect(page.getByText('stripe.charge', { exact: true })).toBeVisible();
  });

  test('Screenshot der Explore-Seite (live, Dark Mode)', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await expect(page.getByText('payment-gateway', { exact: true })).toBeVisible();
    await expect(page.getByText('POST /checkout', { exact: true })).toBeVisible();
    await page.waitForTimeout(500);
    await page.screenshot({ path: 'e2e/screenshots/explore-dark.png' });
  });
});
