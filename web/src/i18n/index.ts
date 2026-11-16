// web/src/i18n/index.ts
import { createMemo, createSignal } from "solid-js";
import type {
  I18nDictionary,
  InterpolationParams,
  Locale,
  LocaleMetadata,
  TxKey,
} from "./types";
import {
  DEFAULT_LOCALE,
  dictionaries,
  LOCALE_METADATA,
  SUPPORTED_LOCALES,
} from "./locales";
import { validateAllDictionaries } from "./validator";

const LOCALE_STORAGE_KEY = "hoxi-locale";

function flattenDictionary(
  obj: object,
  prefix = ""
): Record<string, string> {
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(obj)) {
    const fullPath = prefix ? `${prefix}.${key}` : key;
    if (typeof value === "string") {
      result[fullPath] = value;
    } else if (value && typeof value === "object") {
      Object.assign(result, flattenDictionary(value, fullPath));
    }
  }
  return result;
}

// Pre-flatten dictionaries for O(1) instant key resolution
const flatDictionaries: Record<Locale, Record<string, string>> = {
  "zh-CN": flattenDictionary(dictionaries["zh-CN"]),
  "en-US": flattenDictionary(dictionaries["en-US"]),
};

function initialLocale(): Locale {
  try {
    const saved = localStorage.getItem(LOCALE_STORAGE_KEY);
    if (saved === "zh-CN" || saved === "en-US") {
      return saved;
    }
    if (typeof navigator !== "undefined" && navigator.language) {
      if (navigator.language.toLowerCase().startsWith("zh")) {
        return "zh-CN";
      }
    }
  } catch {
    /* storage unavailable */
  }
  return DEFAULT_LOCALE;
}

export const [locale, setLocaleState] = createSignal<Locale>(initialLocale());

export function setLocale(newLocale: Locale) {
  setLocaleState(newLocale);
  try {
    localStorage.setItem(LOCALE_STORAGE_KEY, newLocale);
  } catch {
    /* storage unavailable */
  }
}

export function toggleLocale() {
  setLocale(locale() === "zh-CN" ? "en-US" : "zh-CN");
}

export const currentLocaleMetadata = createMemo<LocaleMetadata>(
  () => LOCALE_METADATA[locale()] ?? LOCALE_METADATA[DEFAULT_LOCALE]
);

/**
 * Fast, reactive translation helper with compile-time key autocomplete and O(1) lookup.
 * When used in JSX: {t("header.applyToMain")}, SolidJS automatically tracks
 * the `locale()` signal read and re-renders text upon language change.
 */
export function t(key: TxKey, params?: InterpolationParams): string {
  const currentLang = locale();
  const dict = flatDictionaries[currentLang] || flatDictionaries[DEFAULT_LOCALE];
  let text = dict[key] ?? flatDictionaries["en-US"]?.[key] ?? flatDictionaries["zh-CN"]?.[key];

  if (text === undefined) {
    if (import.meta.env?.DEV) {
      console.warn(`[i18n] Missing translation key: "${key}" for locale "${currentLang}"`);
    }
    text = key;
  }

  if (params) {
    return text.replace(/\{(\w+)\}/g, (match, paramName) => {
      return paramName in params ? String(params[paramName]) : match;
    });
  }

  return text;
}

// Development parity assertion: report any missing keys or parameter mismatches in dev console
if (import.meta.env?.DEV) {
  try {
    const parityResults = validateAllDictionaries(dictionaries);
    for (const [loc, res] of Object.entries(parityResults)) {
      if (!res.valid) {
        console.warn(`[i18n] Parity discrepancies detected in locale "${loc}":`, res);
      }
    }
  } catch (err) {
    console.error("[i18n] Validation failed:", err);
  }
}

export * from "./types";
export * from "./locales";
export * from "./validator";
