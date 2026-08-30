// Click a main tab by visible text, then raw-CDP screenshot.
import { connect, resolveOut, disconnect } from './_lib.mjs';
import fs from 'node:fs';
const label = process.argv[2];
const outFile = resolveOut(process.argv[3] ?? 'tabshot.png');
const { browser, page } = await connect();
if (label) {
  const btn = page.locator(`[role="tablist"] >> text=${label}`).first();
  await btn.click({ timeout: 15000 }).catch(e => console.error('click failed:', e.message));
  await page.waitForTimeout(4000);
}
const cdp = await page.context().newCDPSession(page);
const { data } = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
fs.writeFileSync(outFile, Buffer.from(data, 'base64'));
console.log(outFile, fs.statSync(outFile).size);
await disconnect(browser);
