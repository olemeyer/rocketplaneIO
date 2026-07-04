import { test, expect } from '@playwright/test';

test.describe('rocketplane — Landing', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
  });

  test('zeigt die Hero-Headline', async ({ page }) => {
    await expect(page.getByRole('heading', { level: 1 })).toContainText('speed of thought');
  });

  test('nennt die drei Kernversprechen', async ({ page }) => {
    for (const t of ['Keyboard-first', 'OpenTelemetry-native', 'Self-hostable']) {
      await expect(page.getByRole('heading', { name: t })).toBeVisible();
    }
  });

  test('App-Shell zeigt Service-Health', async ({ page }) => {
    await expect(page.getByText('payment-gateway')).toBeVisible();
    await expect(page.getByText('cart-service')).toBeVisible();
  });

  test('Command-Palette öffnet per ⌘K und schließt per Esc', async ({ page }) => {
    // Erst auf Interaktivität warten (Listener wird nach dem React-Mount aktiv).
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    await page.keyboard.press('Control+KeyK');
    const dialog = page.getByRole('dialog', { name: /command menu/i });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByPlaceholder(/type a command/i)).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
  });

  test('Screenshot der Landing (Dark Mode)', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 960 });
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    await page.waitForTimeout(600); // Fonts + Aurora settle
    await page.screenshot({ path: 'e2e/screenshots/home-dark.png' });
  });

  test('Screenshot mit geöffneter Command-Palette', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 960 });
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    await page.keyboard.press('Control+KeyK');
    await expect(page.getByRole('dialog')).toBeVisible();
    await page.waitForTimeout(300);
    await page.screenshot({ path: 'e2e/screenshots/home-palette.png' });
  });
});
