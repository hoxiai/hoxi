// web/scripts/check-i18n.ts
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

if (hasError) {
  console.error("\n[i18n-check] ✗ Validation failed! Please fix the discrepancies above.\n");
  process.exit(1);
} else {
  console.log("\n[i18n-check] ✓ All language packs are 100% synchronized and valid.\n");
  process.exit(0);
}
