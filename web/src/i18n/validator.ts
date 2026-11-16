// web/src/i18n/validator.ts
import type { I18nDictionary, Locale } from "./types";

export interface ParityValidationResult {
  valid: boolean;
  missingInTarget: string[];
  missingInBase: string[];
  paramMismatches: string[];
  emptyKeys: string[];
}

function extractParams(str: string): string[] {
  const matches = str.match(/\{(\w+)\}/g);
  if (!matches) return [];
  return Array.from(new Set(matches.map((m) => m.slice(1, -1)))).sort();
}

/**
 * Validates key parity, missing keys, and placeholder parameters between two dictionaries.
 */
export function validateLocaleParity(
  base: object,
  target: object,
  baseName = "base",
  targetName = "target",
  prefix = ""
): ParityValidationResult {
  const missingInTarget: string[] = [];
  const missingInBase: string[] = [];
  const paramMismatches: string[] = [];
  const emptyKeys: string[] = [];

  const baseObj = base as Record<string, unknown>;
  const targetObj = target as Record<string, unknown>;
  const baseKeys = Object.keys(baseObj);
  const targetKeys = Object.keys(targetObj);

  for (const key of baseKeys) {
    const fullPath = prefix ? `${prefix}.${key}` : key;
    if (!(key in targetObj)) {
      missingInTarget.push(fullPath);
      continue;
    }

    const bVal = baseObj[key];
    const tVal = targetObj[key];

    if (typeof bVal === "object" && bVal !== null && !Array.isArray(bVal)) {
      if (typeof tVal !== "object" || tVal === null || Array.isArray(tVal)) {
        missingInTarget.push(`${fullPath} (expected namespace object)`);
      } else {
        const sub = validateLocaleParity(bVal, tVal, baseName, targetName, fullPath);
        missingInTarget.push(...sub.missingInTarget);
        missingInBase.push(...sub.missingInBase);
        paramMismatches.push(...sub.paramMismatches);
        emptyKeys.push(...sub.emptyKeys);
      }
    } else if (typeof bVal === "string") {
      if (typeof tVal !== "string") {
        missingInTarget.push(`${fullPath} (expected string translation)`);
      } else {
        if (!bVal.trim()) emptyKeys.push(`${baseName}:${fullPath}`);
        if (!tVal.trim()) emptyKeys.push(`${targetName}:${fullPath}`);

        const bParams = extractParams(bVal);
        const tParams = extractParams(tVal);
        if (bParams.join(",") !== tParams.join(",")) {
          paramMismatches.push(
            `${fullPath}: ${baseName} has [${bParams.join(", ")}], but ${targetName} has [${tParams.join(", ")}]`
          );
        }
      }
    }
  }

  for (const key of targetKeys) {
    const fullPath = prefix ? `${prefix}.${key}` : key;
    if (!(key in base)) {
      missingInBase.push(fullPath);
    }
  }

  const valid =
    missingInTarget.length === 0 &&
    missingInBase.length === 0 &&
    paramMismatches.length === 0 &&
    emptyKeys.length === 0;

  return {
    valid,
    missingInTarget,
    missingInBase,
    paramMismatches,
    emptyKeys,
  };
}

/**
 * Validates all loaded dictionaries against the canonical en-US dictionary.
 */
export function validateAllDictionaries(
  dictionaries: Record<Locale, I18nDictionary>
): Record<string, ParityValidationResult> {
  const canonical = dictionaries["en-US"];
  const results: Record<string, ParityValidationResult> = {};

  for (const [loc, dict] of Object.entries(dictionaries)) {
    if (loc === "en-US") continue;
    results[loc] = validateLocaleParity(canonical, dict, "en-US", loc);
  }

  return results;
}
