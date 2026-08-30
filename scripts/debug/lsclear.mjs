// Remove localStorage keys matching a substring, in a fresh tab.
// Usage: node scripts/debug/lsclear.mjs <substring> [url]
import { connect, disconnect } from './_lib.mjs';
const needle = process.argv[2];
const url = process.argv[3] ?? 'http://localhost:1420';
if (!needle) {
  console.error('usage: node lsclear.mjs <substring> [url]');
  process.exit(2);
}
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();
try {
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(6000);
  const removed = await p.evaluate((n) => {
    const hits = Object.keys(localStorage).filter((k) => k.includes(n));
    hits.forEach((k) => localStorage.removeItem(k));
    return hits;
  }, needle);
  console.log('removed:', JSON.stringify(removed));
} finally {
  await p.close();
  await disconnect(browser);
}
