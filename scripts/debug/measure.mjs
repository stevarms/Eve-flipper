// Dump bounding rect + key computed styles for every element matching a selector.
// Prints JSON to stdout and writes to outFile. Numerical measurement beats eyeballing.
// Usage: node scripts/debug/measure.mjs <selector> [route] [outFile]
//   node scripts/debug/measure.mjs ".order-row" /station rows.json
import { connect, gotoRoute, resolveOut, disconnect } from './_lib.mjs';
import { writeFileSync } from 'node:fs';

const selector = process.argv[2];
if (!selector) {
  console.error('usage: node measure.mjs <selector> [route] [outFile]');
  process.exit(2);
}
const route = process.argv[3] ?? '/';
const outFile = resolveOut(process.argv[4] ?? 'measure.json');

const { browser, page } = await connect();
await gotoRoute(page, route);

const data = await page.$$eval(selector, (nodes) => {
  const pick = [
    'display', 'position', 'boxSizing',
    'width', 'height', 'minWidth', 'maxWidth',
    'padding', 'margin', 'border', 'borderRadius',
    'fontFamily', 'fontSize', 'fontWeight', 'lineHeight', 'letterSpacing',
    'color', 'backgroundColor', 'opacity',
    'textAlign', 'verticalAlign', 'whiteSpace', 'overflow',
    'gap', 'rowGap', 'columnGap',
    'justifyContent', 'alignItems', 'alignSelf', 'flexDirection', 'flexWrap',
    'gridTemplateColumns', 'gridTemplateRows',
  ];
  return nodes.map((n, i) => {
    const r = n.getBoundingClientRect();
    const cs = getComputedStyle(n);
    const styles = Object.fromEntries(pick.map((k) => [k, cs[k]]));
    return {
      index: i,
      tag: n.tagName.toLowerCase(),
      id: n.id || undefined,
      classes: typeof n.className === 'string' && n.className ? n.className : undefined,
      text: (n.innerText ?? '').slice(0, 160),
      rect: {
        x: +r.x.toFixed(2), y: +r.y.toFixed(2),
        w: +r.width.toFixed(2), h: +r.height.toFixed(2),
        right: +r.right.toFixed(2), bottom: +r.bottom.toFixed(2),
      },
      styles,
    };
  });
});

const json = JSON.stringify({ selector, route, count: data.length, elements: data }, null, 2);
writeFileSync(outFile, json);
console.log(json);
console.error(`\n(wrote ${outFile})`);
await disconnect(browser);
