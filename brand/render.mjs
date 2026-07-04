import { chromium } from '@playwright/test';
import { fileURLToPath } from 'node:url';

const dir = fileURLToPath(new URL('.', import.meta.url));
const browser = await chromium.launch();
const page = await browser.newPage({ deviceScaleFactor: 2 });
await page.goto(`file://${dir}preview.html`, { waitUntil: 'networkidle' });
await page.waitForTimeout(300);
const body = await page.$('body');
await body.screenshot({ path: `${dir}icons-preview.png` });
await browser.close();
console.log('rendered icons-preview.png');
