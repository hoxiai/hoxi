// web/src/i18n/locales/index.ts
import type { I18nDictionary, Locale, LocaleMetadata } from "../types";
import { enUS } from "./en-US";
import { zhCN } from "./zh-CN";

export { enUS, zhCN };

export const SUPPORTED_LOCALES: readonly Locale[] = ["zh-CN", "en-US"] as const;

export const DEFAULT_LOCALE: Locale = "zh-CN";

export const LOCALE_METADATA: Record<Locale, LocaleMetadata> = {
  "zh-CN": {
    id: "zh-CN",
    label: "简体中文",
    shortLabel: "中文",
    langTag: "zh-CN",
  },
  "en-US": {
    id: "en-US",
    label: "English",
    shortLabel: "EN",
    langTag: "en-US",
  },
};

export const dictionaries: Record<Locale, I18nDictionary> = {
  "zh-CN": zhCN,
  "en-US": enUS,
};
