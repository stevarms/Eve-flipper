// Does DOM accumulate as you navigate?
//
// The pre-overhaul app kept every tab mounted behind a CSS `hidden` class,
// so whatever you loaded in one tab stayed in the document forever: 44,773
// elements / 1,539 rows measured in a real session. This scans Station Trade
// to get a heavy tab populated, then walks every workspace and re-counts.
//
// Pass: the count returns to roughly its pre-scan level once you navigate
// away, instead of carrying the scanned rows around.
import { connect, disconnect } from './_lib.mjs';

const url = process.argv[2] ?? 'http://localhost:1420';
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

const count = () =>
  p.evaluate(() => ({
    els: document.querySelectorAll('*').length,
    rows: document.querySelectorAll('tbody tr').length,
  }));

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);
  console.log('cold load          ', JSON.stringify(await count()));

  await p.getByRole('tab', { name: 'Station Trade' }).first().click({ timeout: 10000 });
  await p.waitForTimeout(2000);
  await p.getByRole('button', { name: 'Jita', exact: true }).first().click({ timeout: 8000 }).catch(() => {});
  await p.waitForTimeout(2000);
  await p.getByRole('button', { name: /^scan$/i }).last().click({ timeout: 10000 });
  for (let i = 0; i < 40; i++) {
    await p.waitForTimeout(5000);
    if ((await p.evaluate(() => document.querySelectorAll('tbody tr').length)) > 1) break;
  }
  console.log('station scanned    ', JSON.stringify(await count()));

  const rail = p.locator('nav[aria-label="Workspaces"] button');
  const n = await rail.count();
  for (let i = 0; i < n; i++) {
    const label = await rail.nth(i).getAttribute('aria-label');
    await rail.nth(i).click({ timeout: 5000 }).catch(() => {});
    await p.waitForTimeout(2500);
    console.log(`  -> ${String(label).padEnd(10)}`, JSON.stringify(await count()));
  }

  console.log('back on Trade      ', JSON.stringify(await count()));
} finally {
  await p.close();
  await disconnect(browser);
}
