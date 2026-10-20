// web/src/i18n/types.ts
import type { enUS } from "./locales/en-US";

type WidenStringLeaves<T> = {
  [K in keyof T]: T[K] extends string ? string : WidenStringLeaves<T[K]>;
};

export type I18nDictionary = WidenStringLeaves<typeof enUS>;

export type CommonI18n = I18nDictionary["common"];
export type HeaderI18n = I18nDictionary["header"];
export type LeftNavI18n = I18nDictionary["leftNav"];
export type TimelineI18n = I18nDictionary["timeline"];
export type ToolCardI18n = I18nDictionary["toolCard"];
export type ApprovalI18n = I18nDictionary["approval"];
export type InspectorI18n = I18nDictionary["inspector"];
export type ModelsI18n = I18nDictionary["models"];
export type SettingsI18n = I18nDictionary["settings"];
export type NetworkI18n = I18nDictionary["network"];
export type SkillsI18n = I18nDictionary["skills"];
export type CommandPaletteI18n = I18nDictionary["commandPalette"];

export type Locale = "zh-CN" | "en-US";

export interface LocaleMetadata {
  id: Locale;
  label: string;
  shortLabel: string;
  langTag: string;
}

type Join<K, P> = K extends string | number
  ? P extends string | number
    ? `${K}${"" extends P ? "" : "."}${P}`
    : never
  : never;

type Leaves<T, D extends number = 5> = [D] extends [0]
  ? never
  : T extends object
  ? { [K in keyof T]-?: Join<K, Leaves<T[K], [-1, 0, 1, 2, 3, 4][D]>> }[keyof T]
  : "";

export type TxKeyPath = Leaves<I18nDictionary>;

/**
 * Autocomplete-friendly and compile-time validated translation key type.
 * Enforces exact paths like `common.ready` or `header.applyToMain` derived from en-US.
 */
export type TxKey = TxKeyPath;

export type InterpolationParams = Record<string, string | number>;
