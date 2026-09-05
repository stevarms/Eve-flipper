// Adds a manual holding through the UI and reports the row it produces.
//
// This is the plan's acceptance test for Assets -> Positions, and it runs
// without EVE SSO: manual rows are readable and writable with only a local
// user, and they are priced from public market data like any other row.
//
// Usage: node scripts/debug/positions-add.mjs [url] [item] [qty] [unitCost]
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const url = process.argv[2] ?? 'http://localhost:13370';
const item = process.argv[3] ?? 'Tritanium';
const qty = process.argv[4] ?? '1000000';
const cost = process.argv[5] ?? '4';

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  await p.locator('nav[aria-label="Workspaces"] button[aria-label="Assets"]').click({ timeout: 10000 });
  await p.waitForTimeout(1200);
  await p.locator('[role="tablist"][aria-label="Workspace tabs"] [role="tab"]', { hasText: /^Positions$/ })
    .first().click({ timeout: 10000 });
  await p.waitForTimeout(2500);

  await p.locator('button', { hasText: /add holding/i }).first().click({ timeout: 10000 });
  await p.waitForTimeout(1000);

  const sheet = p.locator('[role="dialog"]').last();
  await sheet.locator('input').first().fill(item);
  await sheet.locator('button', { hasText: /^Find$/ }).click({ timeout: 8000 });
  await p.waitForTimeout(2500);

  const inputs = sheet.locator('input');
  await inputs.nth(1).fill(qty);   // quantity
  await inputs.nth(2).fill(cost);  // unit cost
  await sheet.locator('button', { hasText: /^Save$/ }).click({ timeout: 8000 });
  await p.waitForTimeout(5000);

  const rows = await p.locator('tbody tr').evaluateAll((trs) =>
    trs.map((tr) => [...tr.querySelectorAll('td')].map((td) => td.innerText.replace(/\s+/g, ' ').trim())));
  console.log('rows              ', JSON.stringify(rows, null, 1));

  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync('.debug/positions-row.png', Buffer.from(data, 'base64'));
  console.log('  shot -> .debug/positions-row.png');
} finally {
  await p.close();
  await disconnect(browser);
}
