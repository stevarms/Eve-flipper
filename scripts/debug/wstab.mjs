// Click a workspace-tab by visible label in a NEW tab, then screenshot.
// Usage: node scripts/debug/wstab.mjs "Station Trade" out.png [url]
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const label = process.argv[2];
const out = process.argv[3] ?? 'wstab.png';
const url = process.argv[4] ?? 'http://localhost:1420';

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();
try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);
  if (label) {
    await p.getByRole('tab', { name: label }).first().click({ timeout: 10000 })
      .catch((e) => console.error('tab click failed:', e.message));
    await p.waitForTimeout(4000);
  }
  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(`.debug/${out}`, Buffer.from(data, 'base64'));
  console.log('ok ->', out, JSON.stringify(await p.evaluate(() => ({
    els: document.querySelectorAll('*').length,
    rows: document.querySelectorAll('tr').length,
  }))));
} finally {
  await p.close();
  await disconnect(browser);
}
