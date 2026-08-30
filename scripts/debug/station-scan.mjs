// Drive a real Station Trade scan and screenshot the resulting grid.
// Usage: node scripts/debug/station-scan.mjs [out.png] [url]
import { connect, disconnect } from './_lib.mjs';
import fs from 'node:fs';

const out = process.argv[2] ?? 'station-scan.png';
const url = process.argv[3] ?? 'http://localhost:1420';

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

const shoot = async (name) => {
  const cdp = await ctx.newCDPSession(p);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(`.debug/${name}`, Buffer.from(data, 'base64'));
};

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  await p.getByRole('tab', { name: 'Station Trade' }).first().click({ timeout: 10000 });
  await p.waitForTimeout(2500);

  // Jita 4-4 specifically: "All stations" over the whole system is a much
  // bigger pull and often times out against live ESI.
  await p.getByRole('button', { name: 'Jita', exact: true }).first().click({ timeout: 8000 })
    .catch((e) => console.error('hub click:', e.message));
  await p.waitForTimeout(2500);

  await p.getByRole('button', { name: /^scan$/i }).last().click({ timeout: 10000 });
  console.log('scan started');

  // Poll for rows rather than sleeping a fixed amount.
  for (let i = 0; i < 60; i++) {
    await p.waitForTimeout(5000);
    const rows = await p.evaluate(() => document.querySelectorAll('tbody tr').length);
    const els = await p.evaluate(() => document.querySelectorAll('*').length);
    if (i % 3 === 0 || rows > 1) console.log(`  t+${(i + 1) * 5}s rows=${rows} els=${els}`);
    if (rows > 1) break;
  }

  await shoot(out);
  console.log('ok ->', out, JSON.stringify(await p.evaluate(() => ({
    els: document.querySelectorAll('*').length,
    bodyRows: document.querySelectorAll('tbody tr').length,
    cols: document.querySelectorAll('thead th').length,
  }))));
} finally {
  await p.close();
  await disconnect(browser);
}
