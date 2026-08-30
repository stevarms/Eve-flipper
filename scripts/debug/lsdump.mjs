// Dump the station column prefs from localStorage in a fresh tab.
import { connect, disconnect } from './_lib.mjs';
const url = process.argv[2] ?? 'http://localhost:1420';
const { browser, page } = await connect();
const ctx = page.context();
const p = await ctx.newPage();
try {
  await p.goto(url, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await p.waitForTimeout(9000);
  console.log(await p.evaluate(() =>
    JSON.stringify(
      Object.keys(localStorage)
        .filter((k) => k.includes('station') || k.includes('column'))
        .reduce((acc, k) => ({ ...acc, [k]: localStorage.getItem(k) }), {}),
      null,
      2,
    ),
  ));
} finally {
  await p.close();
  await disconnect(browser);
}
