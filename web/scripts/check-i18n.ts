// web/scripts/check-i18n.ts
import fs from "node:fs";
import path from "node:path";
import { dictionaries, SUPPORTED_LOCALES } from "../src/i18n/locales";
import { validateAllDictionaries } from "../src/i18n/validator";

console.log(`[i18n-check] Validating i18n locales: ${SUPPORTED_LOCALES.join(", ")}...`);

const results = validateAllDictionaries(dictionaries);
let hasError = false;

for (const [locale, res] of Object.entries(results)) {
  if (res.valid) {
    console.log(`  ✓ [${locale}] All keys and placeholders match en-US`);
    continue;
  }

  hasError = true;
  console.error(`\n  ✗ [${locale}] Discrepancies found:`);

  if (res.missingInTarget.length > 0) {
    console.error(`    Missing in ${locale} (${res.missingInTarget.length}):`);
    for (const key of res.missingInTarget) {
      console.error(`      - ${key}`);
    }
  }

  if (res.missingInBase.length > 0) {
    console.error(`    Unused in en-US but present in ${locale} (${res.missingInBase.length}):`);
    for (const key of res.missingInBase) {
      console.error(`      - ${key}`);
    }
  }

  if (res.paramMismatches.length > 0) {
    console.error(`    Placeholder parameter mismatches (${res.paramMismatches.length}):`);
    for (const err of res.paramMismatches) {
      console.error(`      - ${err}`);
    }
  }

  if (res.emptyKeys.length > 0) {
    console.error(`    Empty translation strings (${res.emptyKeys.length}):`);
    for (const err of res.emptyKeys) {
      console.error(`      - ${err}`);
    }
  }
}

// Flatten canonical dictionary
function flatten(obj: object, prefix = ""): Set<string> {
  const keys = new Set<string>();
  for (const [k, v] of Object.entries(obj)) {
    const full = prefix ? `${prefix}.${k}` : k;
    if (typeof v === "string") {
      keys.add(full);
    } else if (v && typeof v === "object") {
      for (const sub of flatten(v, full)) {
        keys.add(sub);
      }
    }
  }
  return keys;
}

const canonicalKeys = flatten(dictionaries["en-US"]);

// Scan source files for t(...) usage and accidental Chinese literals
console.log("\n[i18n-check] Scanning source code for translation key coverage and raw literals...");

function walkDir(dir: string): string[] {
  let files: string[] = [];
  for (const item of fs.readdirSync(dir)) {
    const full = path.join(dir, item);
    if (fs.statSync(full).isDirectory()) {
      if (!full.includes("node_modules") && !full.includes("i18n") && !full.includes("dist")) {
        files = files.concat(walkDir(full));
      }
    } else if (/\.(tsx?|jsx?)$/.test(item)) {
      files.push(full);
    }
  }
  return files;
}

const webDir = fs.existsSync("./src") ? path.resolve(".") : path.resolve("./web");
const srcDir = path.join(webDir, "src");
const srcFiles = walkDir(srcDir);
const missingInDictionary: Array<{ file: string; key: string }> = [];
const hardcodedChineseMatches: Array<{ file: string; line: number; text: string }> = [];

const tCallRegex = /\bt\(\s*["']([a-zA-Z0-9_.]+)["']/g;
const chineseRegex = /[\u4e00-\u9fa5]/;

for (const file of srcFiles) {
  const content = fs.readFileSync(file, "utf8");
  const relFile = path.relative(webDir, file);

  // Check t("key") usage
  let match: RegExpExecArray | null;
  while ((match = tCallRegex.exec(content)) !== null) {
    const key = match[1];
    if (!canonicalKeys.has(key)) {
      missingInDictionary.push({ file: relFile, key });
    }
  }

  // Check hardcoded Chinese
  const lines = content.split("\n");
  lines.forEach((lineText, idx) => {
    const trimmed = lineText.trim();
    if (trimmed.startsWith("//") || trimmed.startsWith("/*") || trimmed.startsWith("*")) return;
    if (chineseRegex.test(trimmed)) {
      hardcodedChineseMatches.push({ file: relFile, line: idx + 1, text: trimmed });
    }
  });
}

if (missingInDictionary.length > 0) {
  hasError = true;
  console.error(`\n  ✗ Found ${missingInDictionary.length} translation keys used in code but missing from dictionary:`);
  for (const item of missingInDictionary) {
    console.error(`    - ${item.file}: "${item.key}"`);
  }
} else {
  console.log("  ✓ All translation keys referenced in codebase exist in the dictionary");
}

if (hardcodedChineseMatches.length > 0) {
  hasError = true;
  console.error(`\n  ✗ Found ${hardcodedChineseMatches.length} hardcoded Chinese strings in source code (should use i18n):`);
  for (const item of hardcodedChineseMatches) {
    console.error(`    - ${item.file}:${item.line}: ${item.text}`);
  }
} else {
  console.log("  ✓ Zero hardcoded Chinese literals in source code outside i18n locales");
}

// zh-CN translations must not end with a full stop
function collectTrailingFullStops(obj: object, prefix = ""): string[] {
  const keys: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const full = prefix ? `${prefix}.${k}` : k;
    if (typeof v === "string") {
      if (/。\s*$/.test(v)) keys.push(full);
    } else if (v && typeof v === "object") {
      keys.push(...collectTrailingFullStops(v, full));
    }
  }
  return keys;
}

const trailingFullStops = collectTrailingFullStops(dictionaries["zh-CN"]);

if (trailingFullStops.length > 0) {
  hasError = true;
  console.error(`\n  ✗ Found ${trailingFullStops.length} zh-CN strings ending with "。" (remove the trailing full stop):`);
  for (const key of trailingFullStops) {
    console.error(`    - ${key}`);
  }
} else {
  console.log('  ✓ No zh-CN translation strings end with "。"');
}

if (hasError) {
  console.error("\n[i18n-check] ✗ Validation failed! Please fix the discrepancies above.\n");
  process.exit(1);
} else {
  console.log("\n[i18n-check] ✓ All language packs and source usages are 100% synchronized and valid.\n");
  process.exit(0);
}
