// Open the Industry workspace, optionally pick a sub-tab, and screenshot.
// Usage: node scripts/debug/industry-shot.mjs [subTabLabel] [out.png] [url]
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const sub = process.argv[2] || '';
const out = process.argv[3] ?? 'industry.png';
const url = process.argv[4] ?? 'http://localhost:1420';

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  await p.locator('nav[aria-label="Workspaces"] button[aria-label="Industry"]').click({ timeout: 10000 });
  await p.waitForTimeout(3000);

  if (sub) {
    await p.getByRole('tab', { name: sub }).first().click({ timeout: 10000 })
      .catch((e) => console.error('sub-tab click failed:', e.message));
    await p.waitForTimeout(3000);
  }

  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(`.debug/${out}`, Buffer.from(data, 'base64'));
  console.log('ok ->', out, JSON.stringify(await p.evaluate(() => ({
    els: document.querySelectorAll('*').length,
  }))));
} finally {
  await p.close();
  await disconnect(browser);
}
