// web/src/__tests__/i18n.test.ts
import {
  dictionaries,
  locale,
  setLocale,
  t,
  toggleLocale,
  validateAllDictionaries,
  validateLocaleParity,
} from "../i18n";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("i18n translation & parity", () => {
  it("resolves keys across en-US and zh-CN and interpolates placeholders", () => {
    setLocale("en-US");
    assertEqual(locale(), "en-US");
    assertEqual(t("common.cancel"), dictionaries["en-US"].common.cancel);
    assertEqual(
      t("timeline.thoughtFor", { duration: "2.4s" }),
      dictionaries["en-US"].timeline.thoughtFor.replace("{duration}", "2.4s")
    );

    toggleLocale();
    assertEqual(locale(), "zh-CN");
    assertEqual(t("common.cancel"), dictionaries["zh-CN"].common.cancel);
    assertEqual(
      t("timeline.thoughtFor", { duration: "2.4s" }),
      dictionaries["zh-CN"].timeline.thoughtFor.replace("{duration}", "2.4s")
    );
  });

  it("validates 100% key and placeholder parity between en-US and zh-CN", () => {
    const results = validateAllDictionaries(dictionaries);
    for (const [loc, res] of Object.entries(results)) {
      assertTrue(res.valid, `expected locale ${loc} to have 100% parity with en-US`);
      assertEqual(res.missingInTarget.length, 0);
      assertEqual(res.missingInBase.length, 0);
      assertEqual(res.paramMismatches.length, 0);
      assertEqual(res.emptyKeys.length, 0);
    }
  });

  it("detects missing keys and mismatched interpolation placeholders", () => {
    const base = {
      greeting: "Hello {name}!",
      nested: { count: "{n} items" },
    };
    const broken = {
      greeting: "Hola {user}!",
      extraKey: "extra",
    };
    const res = validateLocaleParity(base, broken, "en-US", "es-ES");
    assertEqual(res.valid, false);
    assertEqual(res.missingInTarget.length, 1);
    assertEqual(res.missingInBase.length, 1);
    assertEqual(res.paramMismatches.length, 1);
  });
});
