// Open a URL in a NEW tab, click through every workspace in the rail, and
// report DOM element / table-row counts at each step.
//
// This is the pass/fail instrument for the UI overhaul's headline metric:
// the pre-overhaul app kept all 11 tabs mounted behind a CSS `hidden` class
// and measured 44,773 elements / 1,539 rows at once.
//
// Usage: node scripts/debug/domcount.mjs [url]
import { connect, disconnect } from './_lib.mjs';

const url = process.argv[2] ?? 'http://localhost:1420';
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(10000);

  const count = () =>
    p.evaluate(() => ({
      els: document.querySelectorAll('*').length,
      rows: document.querySelectorAll('tr').length,
    }));

  console.log('on load           ', JSON.stringify(await count()));

  const rail = p.locator('nav[aria-label="Workspaces"] button');
  const n = await rail.count();
  console.log('rail workspaces   ', n);

  for (let i = 0; i < n; i++) {
    const label = await rail.nth(i).getAttribute('aria-label');
    await rail.nth(i).click({ timeout: 5000 }).catch(() => {});
    await p.waitForTimeout(1500);
    console.log(`  ${String(label).padEnd(16)}`, JSON.stringify(await count()));
  }

  console.log('after visiting all', JSON.stringify(await count()));
} finally {
  await p.close();
  await disconnect(browser);
}
