// Open an external URL in a NEW tab of the debug Chrome, screenshot, close tab.
// Leaves the app tab untouched.
import { connect, resolveOut, disconnect } from './_lib.mjs';
import fs from 'node:fs';
const url = process.argv[2];
const outFile = resolveOut(process.argv[3] ?? 'ext.png');
const waitMs = Number(process.argv[4] ?? 9000);
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();
try {
  await p.setViewportSize({ width: 1600, height: 1100 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(waitMs);
  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(outFile, Buffer.from(data, 'base64'));
  console.log(outFile, fs.statSync(outFile).size, '|', await p.title());
} finally {
  await p.close();
  await disconnect(browser);
}
