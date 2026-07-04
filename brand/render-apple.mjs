import { chromium } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const dir = fileURLToPath(new URL('.', import.meta.url));
const out = fileURLToPath(new URL('../apps/web/app/apple-icon.png', import.meta.url));

let svg = readFileSync(`${dir}logo-mark.svg`, 'utf8').trim();
// feste Größe für ein 180x180-Element-Screenshot
svg = svg.replace('<svg ', '<svg width="180" height="180" ');

const browser = await chromium.launch();
const page = await browser.newPage();
await page.setContent(`<!doctype html><style>*{margin:0}</style>${svg}`);
await page.locator('svg').screenshot({ path: out });
await browser.close();
console.log('apple-icon.png ->', out);
