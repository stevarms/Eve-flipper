// Reports every HTTP request that came back >=400 while sweeping the tabs.
//
// The tab sweep counts console errors but not their URLs, and a bare
// "Failed to load resource: the server responded with a status of 400" is
// unactionable without knowing which request produced it.
//
// Usage: node scripts/debug/failed-req.mjs [url]
import { connect, disconnect } from './_lib.mjs';

const url = process.argv[2] ?? 'http://localhost:13370';
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();

const bad = [];
p.on('response', (r) => {
  if (r.status() >= 400) bad.push(`${r.status()} ${r.request().method()} ${r.url().slice(0, 150)}`);
});

try {
  await p.setViewportSize({ width: 1600, height: 1000 });
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);

  const rail = p.locator('nav[aria-label="Workspaces"] button');
  const nWs = await rail.count();
  for (let i = 0; i < nWs; i++) {
    await rail.nth(i).click({ timeout: 5000 }).catch(() => {});
    await p.waitForTimeout(900);
    const tabs = p.locator('[role="tablist"][aria-label="Workspace tabs"] [role="tab"]');
    const nTabs = await tabs.count();
    for (let j = 0; j < nTabs; j++) {
      await tabs.nth(j).click({ timeout: 5000 }).catch(() => {});
      await p.waitForTimeout(1500);
    }
  }

  console.log(bad.length ? [...new Set(bad)].join('\n') : 'no failed requests');
} finally {
  await p.close();
  await disconnect(browser);
}
