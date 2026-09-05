// Deletes manual holdings through the drawer, one at a time, until none are
// left. Exercises DELETE /api/auth/positions/{id} end to end and doubles as
// the cleanup for whatever positions-add.mjs left behind.
//
// Usage: node scripts/debug/positions-delete.mjs [url] [keep]
//   keep — how many rows to leave in place (default 0).
import { connect, disconnect } from './_lib.mjs';

const url = process.argv[2] ?? 'http://localhost:13370';
const keep = Number(process.argv[3] ?? 0);

const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  await p.locator('nav[aria-label="Workspaces"] button[aria-label="Assets"]').click({ timeout: 10000 });
  await p.waitForTimeout(1200);
  await p.locator('[role="tablist"][aria-label="Workspace tabs"] [role="tab"]', { hasText: /^Positions$/ })
    .first().click({ timeout: 10000 });
  await p.waitForTimeout(3000);

  for (let guard = 0; guard < 20; guard++) {
    const n = await p.locator('tbody tr').count();
    console.log('rows              ', n);
    if (n <= keep) break;

    await p.locator('tbody tr').first().click({ timeout: 8000 });
    await p.waitForTimeout(1200);
    const del = p.locator('[role="dialog"]').last().locator('button', { hasText: /delete|удал/i }).first();
    if (!(await del.count())) { console.log('no delete control  (row is FIFO-derived, not manual)'); break; }
    await del.click({ timeout: 8000 });
    await p.waitForTimeout(3000);
  }

  console.log('final rows        ', await p.locator('tbody tr').count());
} finally {
  await p.close();
  await disconnect(browser);
}
