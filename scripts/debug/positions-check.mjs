// Acceptance check for Assets -> Positions, runnable without EVE SSO.
//
// Opens the app in a new tab, walks the rail to Assets, selects the Positions
// tab, screenshots the empty state, then opens the add-manual-holding dialog
// and screenshots that too. Reports what it found on stdout so the run is
// legible without opening the PNGs.
//
// Usage: node scripts/debug/positions-check.mjs [url]
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
  await p.waitForTimeout(1500);

  const tabs = await p.locator('[role="tablist"][aria-label="Workspace tabs"] [role="tab"]').allTextContents();
  console.log('assets tabs       ', JSON.stringify(tabs));

  await p.locator('[role="tablist"][aria-label="Workspace tabs"] [role="tab"]', { hasText: /^Positions$/ })
    .first().click({ timeout: 10000 });
  await p.waitForTimeout(3000);
  console.log('positions tab     ', JSON.stringify(await p.evaluate(() => ({
    els: document.querySelectorAll('*').length,
    rows: document.querySelectorAll('tbody tr').length,
    h: (document.querySelector('main h2, main h1')?.textContent ?? '').trim(),
  }))));
  await shot('positions-empty.png');

  // The add-manual-holding entry point.
  const add = p.locator('button', { hasText: /add holding|добав/i }).first();
  if (await add.count()) {
    await add.click({ timeout: 10000 });
    await p.waitForTimeout(1500);
    console.log('dialog open       ', await p.locator('[role="dialog"]').count() > 0);
    await shot('positions-add.png');
  } else {
    console.log('dialog open        no add button found');
  }
} finally {
  await p.close();
  await disconnect(browser);
}
