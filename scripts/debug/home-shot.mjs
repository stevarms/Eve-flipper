// Open the Today workspace and screenshot it.
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const out = process.argv[2] ?? 'home.png';
const url = process.argv[3] ?? 'http://localhost:1420';

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  await p.locator('nav[aria-label="Workspaces"] button[aria-label="Today"]').click({ timeout: 10000 });
  await p.waitForTimeout(6000);

  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(`.debug/${out}`, Buffer.from(data, 'base64'));
  console.log('ok ->', out, JSON.stringify(await p.evaluate(() => ({
    els: document.querySelectorAll('*').length,
    rows: document.querySelectorAll('tbody tr').length,
  }))));
} finally {
  await p.close();
  await disconnect(browser);
}
