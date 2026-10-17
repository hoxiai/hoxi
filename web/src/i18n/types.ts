// web/src/i18n/types.ts
import type {
  ApprovalI18n,
  CommonI18n,
  HeaderI18n,
  InspectorI18n,
  LeftNavI18n,
  ModelsI18n,
  SettingsI18n,
  TimelineI18n,
  ToolCardI18n,
} from "./domain-types";

export * from "./domain-types";

export type Locale = "zh-CN" | "en-US";

export interface LocaleMetadata {
  id: Locale;
  label: string;
  shortLabel: string;
  langTag: string;
}

export interface I18nDictionary {
  common: CommonI18n;
  header: HeaderI18n;
  leftNav: LeftNavI18n;
  timeline: TimelineI18n;
  toolCard: ToolCardI18n;
  approval: ApprovalI18n;
  inspector: InspectorI18n;
  models: ModelsI18n;
  settings: SettingsI18n;
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
 * Autocomplete-friendly translation key type.
 * Suggests exact paths like `common.ready` or `header.applyToMain`
 * while still permitting dynamic strings if needed.
 */
export type TxKey = TxKeyPath | (string & {});

export type InterpolationParams = Record<string, string | number>;
