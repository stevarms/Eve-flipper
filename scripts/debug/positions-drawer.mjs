// Screenshots the Positions grid and then its row drawer.
// Usage: node scripts/debug/positions-drawer.mjs [url]
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const url = process.argv[2] ?? 'http://localhost:13370';
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

const shot = async (name) => {
  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(`.debug/${name}`, Buffer.from(data, 'base64'));
  console.log('  shot ->', `.debug/${name}`);
};

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  await p.locator('nav[aria-label="Workspaces"] button[aria-label="Assets"]').click({ timeout: 10000 });
  await p.waitForTimeout(1200);
  await p.locator('[role="tablist"][aria-label="Workspace tabs"] [role="tab"]', { hasText: /^Positions$/ })
    .first().click({ timeout: 10000 });
  await p.waitForTimeout(3500);

  const cells = await p.locator('tbody tr').first()
    .evaluate((tr) => [...tr.querySelectorAll('td')].map((td) => td.innerText.replace(/\s+/g, ' ').trim()))
    .catch(() => null);
  console.log('first row         ', JSON.stringify(cells));
  await shot('positions-grid.png');

  await p.locator('tbody tr').first().click({ timeout: 8000 });
  await p.waitForTimeout(1500);
  const detail = await p.locator('[role="dialog"]').last()
    .evaluate((el) => el.innerText.replace(/\n+/g, ' | ').trim()).catch(() => null);
  console.log('drawer            ', JSON.stringify(detail));
  await shot('positions-drawer.png');
} finally {
  await p.close();
  await disconnect(browser);
}
