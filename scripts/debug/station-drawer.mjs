// Scan, click the top row, and screenshot the detail drawer.
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const out = process.argv[2] ?? 'station-drawer.png';
const url = process.argv[3] ?? 'http://localhost:1420';

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  await p.getByRole('tab', { name: 'Station Trade' }).first().click({ timeout: 10000 });
  await p.waitForTimeout(2000);
  await p.getByRole('button', { name: 'Jita', exact: true }).first().click({ timeout: 8000 }).catch(() => {});
  await p.waitForTimeout(2000);
  await p.getByRole('button', { name: /^scan$/i }).last().click({ timeout: 10000 });

  for (let i = 0; i < 40; i++) {
    await p.waitForTimeout(5000);
    if ((await p.evaluate(() => document.querySelectorAll('tbody tr').length)) > 1) break;
  }

  // Click the item-name cell of the first row (inert space, not a control).
  await p.locator('tbody tr').first().locator('td').nth(2).click({ timeout: 10000 });
  await p.waitForTimeout(1800);

  const dialog = await p.locator('[role="dialog"]').count();
  console.log('dialog open:', dialog > 0);
  if (dialog > 0) {
    const text = await p.locator('[role="dialog"]').first().innerText();
    console.log('--- drawer content ---');
    console.log(text.split('\n').slice(0, 40).join('\n'));
  }

  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(`.debug/${out}`, Buffer.from(data, 'base64'));
  console.log('ok ->', out);
} finally {
  await p.close();
  await disconnect(browser);
}
