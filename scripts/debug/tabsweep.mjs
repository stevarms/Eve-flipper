// Clicks every tab in every workspace and reports what each one rendered,
// plus any console error or page exception it produced.
//
// This is the check that matters after moving a tool's mount point: the nine
// tools promoted out of the character modal now take their props from
// CharacterScopeProvider instead of CharacterPopup's local state, and a
// mismatch there is a blank tab, not a build error.
//
// Usage: node scripts/debug/tabsweep.mjs [url]
import { connect, disconnect } from './_lib.mjs';

const url = process.argv[2] ?? 'http://localhost:13370';
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

const errors = [];
p.on('console', (m) => { if (m.type() === 'error') errors.push(`console: ${m.text().slice(0, 160)}`); });
p.on('pageerror', (e) => errors.push(`pageerror: ${String(e).slice(0, 160)}`));

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  const rail = p.locator('nav[aria-label="Workspaces"] button');
  const nWs = await rail.count();
  let total = 0;

  for (let i = 0; i < nWs; i++) {
    const ws = await rail.nth(i).getAttribute('aria-label');
    await rail.nth(i).click({ timeout: 5000 }).catch(() => {});
    await p.waitForTimeout(1000);

    const tabs = p.locator('[role="tablist"][aria-label="Workspace tabs"] [role="tab"]');
    const nTabs = await tabs.count();
    console.log(`${ws} (${nTabs} tabs)`);

    for (let j = 0; j < nTabs; j++) {
      const before = errors.length;
      const label = (await tabs.nth(j).innerText()).trim();
      await tabs.nth(j).click({ timeout: 5000 }).catch(() => {});
      await p.waitForTimeout(1800);
      const m = await p.evaluate(() => {
        const main = document.querySelector('main') ?? document.body;
        return { els: main.querySelectorAll('*').length, text: main.innerText.trim().length };
      });
      total++;
      const fresh = errors.slice(before);
      const flag = m.els < 12 || m.text < 20 ? '  <-- LOOKS EMPTY' : '';
      console.log(`  ${label.padEnd(18)} els=${String(m.els).padEnd(5)} chars=${String(m.text).padEnd(6)}${fresh.length ? ` ERR ${fresh[0]}` : ''}${flag}`);
    }
  }

  console.log(`\ntabs visited ${total}, console errors ${errors.length}`);
  for (const e of [...new Set(errors)].slice(0, 10)) console.log('  ', e);
} finally {
  await p.close();
  await disconnect(browser);
}
