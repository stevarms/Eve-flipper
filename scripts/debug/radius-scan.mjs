// Drive a Radius Trade scan and screenshot the grid.
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const out = process.argv[2] ?? 'radius-scan.png';
const url = process.argv[3] ?? 'http://localhost:1420';

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  // The active tab persists, so we may land in any workspace. Select Trade
  // on the rail first, then the tab within it.
  await p.locator('nav[aria-label="Workspaces"] button[aria-label="Trade"]').click({ timeout: 10000 });
  await p.waitForTimeout(1500);
  await p.getByRole('tab', { name: 'Radius Trade' }).first().click({ timeout: 10000 });
  await p.waitForTimeout(2000);

  // The workspace-level Scan button lives in the tab row.
  await p.locator('[data-scan-button]').first().click({ timeout: 10000 });
  console.log('scan started');

  for (let i = 0; i < 60; i++) {
    await p.waitForTimeout(5000);
    const rows = await p.evaluate(() => document.querySelectorAll('tbody tr').length);
    if (i % 4 === 0) console.log(`  t+${(i + 1) * 5}s rows=${rows}`);
    if (rows > 1) break;
  }

  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(`.debug/${out}`, Buffer.from(data, 'base64'));
  console.log('ok ->', out, JSON.stringify(await p.evaluate(() => ({
    els: document.querySelectorAll('*').length,
    bodyRows: document.querySelectorAll('tbody tr').length,
    cols: document.querySelectorAll('thead th').length,
  }))));
} finally {
  await p.close();
  await disconnect(browser);
}
