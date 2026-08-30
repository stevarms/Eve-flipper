// One-off: evaluate a JS expression in the running app's page and dump the
// JSON result to stdout. Useful for pulling out window-exposed globals like
// __ledgerSnapshot without opening DevTools.
// Usage: node scripts/debug/eval.mjs "<expression>"
import { connect, disconnect } from './_lib.mjs';

const expr = process.argv[2];
if (!expr) {
  console.error('usage: node eval.mjs "<js expression>"');
  process.exit(2);
}

const { browser, page } = await connect();
try {
  const result = await page.evaluate(async (code) => {
    // eslint-disable-next-line no-new-func
    const value = (new Function(`return (${code})`))();
    const resolved = value && typeof value.then === "function" ? await value : value;
    return JSON.stringify(resolved, null, 2);
  }, expr);
  console.log(result);
} finally {
  await disconnect(browser);
}
