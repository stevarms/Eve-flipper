// Reload the app, switch to the Assets workspace, open PLEX, screenshot.
import { connect, resolveOut, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const out = resolveOut(process.argv[2] ?? 'plex-tab.png');
const { browser, page } = await connect();
await page.setViewportSize({ width: 1600, height: 1000 }).catch(() => {});
await page.goto(process.env.EF_URL ?? 'http://localhost:13370', { waitUntil: 'domcontentloaded', timeout: 45000 });
await page.waitForTimeout(6000);

const clicked = await page.evaluate(() => {
  const hit = (re) => [...document.querySelectorAll('button,[role="tab"],a')]
    .find((el) => re.test((el.textContent || '').trim()));
  const log = [];
  const ws = hit(/^Assets$/i);
  if (ws) { ws.click(); log.push('assets'); }
  return log;
});
await page.waitForTimeout(1500);
const clicked2 = await page.evaluate(() => {
  const el = [...document.querySelectorAll('button,[role="tab"]')]
    .find((e) => /^PLEX/i.test((e.textContent || '').trim()));
  if (el) { el.click(); return el.textContent.trim(); }
  return null;
});
await page.waitForTimeout(9000);

// Optional third step: one of the arbitrage sub-tabs (NES / Market / Spread).
const arb = process.argv[3];
let clicked3 = null;
if (arb) {
  clicked3 = await page.evaluate((want) => {
    const el = [...document.querySelectorAll('button')]
      .find((e) => (e.textContent || '').trim().toLowerCase().startsWith(want.toLowerCase()));
    if (!el) return null;
    el.click();
    el.scrollIntoView({ block: 'center' });
    return el.textContent.trim();
  }, arb);
  await page.waitForTimeout(2500);
}
console.error('workspace:', clicked, 'tab:', clicked2, 'arb:', clicked3);

const cdp = await page.context().newCDPSession(page);
const { data } = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
fs.writeFileSync(out, Buffer.from(data, 'base64'));
console.log(out, fs.statSync(out).size);
await disconnect(browser);
