export interface ParsedItem {
  name: string;
  qty: number;
}

// Parse a paste of items. Handles three EVE inventory export formats:
//
//   1. List-view copy (TAB-separated):
//        "Tritanium\t1000\tMineral\t0.01 m3"
//      Qty is always column 2; extra columns are ignored.
//
//   2. Detailed-view copy (space-separated with metadata trail):
//        "Common Moon Mining Crystal Type A II 90 Mining Crystal ... 63,510,932.70 ISK"
//      Name is everything before the first purely-numeric token; qty is
//      that token; everything after (type / category / size / meta /
//      estimated price / ISK) is discarded.
//
//   3. Hand-typed:
//        "tritanium 1000"        -> { name: "tritanium",     qty: 1000 }
//        "Compressed Tritanium"  -> { name: "Compressed Tritanium", qty: 1 }
//
// EVE item names can contain alphanumeric tokens ("250mm", "10MN") but
// never a token that is *purely* digits — Roman numerals ("II", "IV") are
// alphabetic. So "first pure-digit token = qty" is a safe heuristic.
export function parseEveClipboard(text: string): ParsedItem[] {
  const lines = text.split(/\r?\n/);
  const out: ParsedItem[] = [];
  for (const raw of lines) {
    const trimmed = raw.trim();
    if (!trimmed) continue;

    // 1. TAB-separated wins when tabs are present.
    if (trimmed.includes("\t")) {
      const parts = trimmed.split(/\t+/);
      const name = parts[0].trim();
      if (!name) continue;
      let qty = 1;
      if (parts.length > 1) {
        const qtyStr = parts[1].replace(/[,\s]/g, "");
        const parsed = Number(qtyStr);
        if (Number.isFinite(parsed) && parsed > 0) qty = Math.floor(parsed);
      }
      out.push({ name, qty });
      continue;
    }

    // 2/3. Whitespace-separated. Walk tokens and find the first that is
    // purely digits (with optional thousands commas).
    const tokens = trimmed.split(/\s+/);
    let qtyIdx = -1;
    for (let i = 1; i < tokens.length; i++) {
      if (/^\d[\d,]*$/.test(tokens[i])) {
        qtyIdx = i;
        break;
      }
    }
    if (qtyIdx === -1) {
      // No qty token — whole line is the name at qty 1.
      out.push({ name: trimmed, qty: 1 });
      continue;
    }
    const name = tokens.slice(0, qtyIdx).join(" ");
    const qtyNum = Math.floor(Number(tokens[qtyIdx].replace(/,/g, "")));
    const qty = Number.isFinite(qtyNum) && qtyNum > 0 ? qtyNum : 1;
    if (name) out.push({ name, qty });
  }
  return out;
}
