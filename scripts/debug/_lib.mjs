import { chromium } from 'playwright-core';
import { mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';

export const BASE = process.env.EF_URL ?? 'http://localhost:13370';
// Use 127.0.0.1 not localhost: Chrome's debug port binds IPv4 only,
// but Node resolves `localhost` to IPv6 (::1) first on Windows -> ECONNREFUSED.
export const CDP = process.env.EF_CDP ?? 'http://127.0.0.1:9222';
const DEBUG_DIR = process.env.EF_DEBUG_DIR ?? '.debug';

export function resolveOut(name) {
  const p = resolve(DEBUG_DIR, name);
  mkdirSync(dirname(p), { recursive: true });
  return p;
}

export async function connect() {
  let browser;
  try {
    browser = await chromium.connectOverCDP(CDP);
  } catch (err) {
    throw new Error(
      `Could not connect to Chrome at ${CDP}. Launch it first with:\n` +
      `  chrome --remote-debugging-port=9222 --user-data-dir=.chrome-debug ${BASE}\n` +
      `Original error: ${err.message}`
    );
  }
  const contexts = browser.contexts();
  if (contexts.length === 0) {
    throw new Error('CDP connected but no browser contexts. Open a tab in Chrome and retry.');
  }
  const ctx = contexts[0];
  let page = ctx.pages().find((p) => p.url().startsWith(BASE));
  if (!page) page = await ctx.newPage();
  return { browser, page };
}

export async function gotoRoute(page, route) {
  // The primary use case is "look at what I already have open." Every
  // page.goto() call in Playwright triggers a real reload — even when the
  // URL is byte-identical to the current one — which for this SPA means
  // login redirects re-fire and tab state resets. So navigate only when
  // the caller genuinely asks for a route we aren't already on.
  //
  // Rule: skip goto if page.url() already starts with BASE + route (after
  // stripping trailing slashes). This treats "/" as "stay on the app" and
  // treats "/industry" as "stay if we're anywhere under /industry."
  const path = route.startsWith('/') ? route : '/' + route;
  const target = (BASE + path).replace(/\/$/, '');
  const current = page.url().replace(/\/$/, '');
  const alreadyThere =
    current === target ||
    (path === '/' && current.startsWith(BASE)) ||
    current.startsWith(target + '/') ||
    current.startsWith(target + '?') ||
    current.startsWith(target + '#');
  if (!alreadyThere) {
    await page.goto(BASE + path, { waitUntil: 'domcontentloaded' });
    await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
  }
}

export async function disconnect(browser) {
  await browser.close();
}
