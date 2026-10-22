// web/src/__tests__/format-units.test.ts
//
// Expected strings are read from the active dictionary instead of being
// written out as literals: the i18n guard treats any Chinese literal in
// source as a hardcoded UI string, and test assertions are no exception.
import { dictionaries, setLocale } from "../i18n";
import { formatCompactNumber, formatExactNumber, formatUsd } from "../i18n/format";
import { assertEqual, describe, it } from "./test-harness";

function unit(locale: "zh-CN" | "en-US", key: "unitK" | "unitM" | "unitB" | "unitT" | "unitWan" | "unitYi" | "unitWanYi"): string {
  return dictionaries[locale].common[key];
}

describe("locale-aware compact units", () => {
  it("uses the Chinese ten-thousand and hundred-million scales", () => {
    setLocale("zh-CN");
    const wan = unit("zh-CN", "unitWan");
    const yi = unit("zh-CN", "unitYi");
    const wanYi = unit("zh-CN", "unitWanYi");

    assertEqual(formatCompactNumber(9_500), "9500");
    assertEqual(formatCompactNumber(12_000), `1.2${wan}`);
    assertEqual(formatCompactNumber(95_000), `9.5${wan}`);
    assertEqual(formatCompactNumber(1_280_000), `128${wan}`);
    assertEqual(formatCompactNumber(81_600_000), `8160${wan}`);
    assertEqual(formatCompactNumber(128_136_000), `1.28${yi}`);
    assertEqual(formatCompactNumber(1_281_360_000), `12.81${yi}`);
    assertEqual(formatCompactNumber(8_160_000_000), `81.6${yi}`);
    assertEqual(formatCompactNumber(309_290_000_000), `3093${yi}`);
    assertEqual(formatCompactNumber(1_500_000_000_000), `1.5${wanYi}`);
  });

  it("keeps k/M/B/T in English", () => {
    setLocale("en-US");
    assertEqual(formatCompactNumber(9_500), `9.5${unit("en-US", "unitK")}`);
    assertEqual(formatCompactNumber(1_280_000), `1.28${unit("en-US", "unitM")}`);
    assertEqual(formatCompactNumber(1_281_360_000), `1.28${unit("en-US", "unitB")}`);
    assertEqual(formatCompactNumber(1_500_000_000_000), `1.5${unit("en-US", "unitT")}`);
  });

  it("never shows a fraction of a unit when the value is huge", () => {
    setLocale("zh-CN");
    const yi = unit("zh-CN", "unitYi");
    assertEqual(formatCompactNumber(816_000_000), `8.16${yi}`);
    assertEqual(formatCompactNumber(81_600_000_000), `816${yi}`);

    setLocale("en-US");
    const million = unit("en-US", "unitM");
    const billion = unit("en-US", "unitB");
    assertEqual(formatCompactNumber(816_000_000), `816${million}`);
    assertEqual(formatCompactNumber(81_600_000_000), `81.6${billion}`);
  });

  it("falls back to a plain integer below the smallest unit", () => {
    setLocale("zh-CN");
    assertEqual(formatCompactNumber(0), "0");
    assertEqual(formatCompactNumber(-5), "0");
    assertEqual(formatCompactNumber(1_200), "1200");

    setLocale("en-US");
    assertEqual(formatCompactNumber(1_200), `1.2${unit("en-US", "unitK")}`);
  });

  it("honours a decimal ceiling for dense UI", () => {
    setLocale("zh-CN");
    assertEqual(formatCompactNumber(128_136_000, { maxDecimals: 1 }), `1.3${unit("zh-CN", "unitYi")}`);

    setLocale("en-US");
    assertEqual(formatCompactNumber(1_281_360_000, { maxDecimals: 1 }), `1.3${unit("en-US", "unitB")}`);
  });

  it("formats exact counts and USD without locale units", () => {
    setLocale("zh-CN");
    assertEqual(formatExactNumber(1_281_360_000), "1,281,360,000");
    assertEqual(formatExactNumber(999), "999");
    assertEqual(formatExactNumber(-1234), "-1,234");
    assertEqual(formatUsd(0), "$0.00");
    assertEqual(formatUsd(0.0004), "<$0.001");
    assertEqual(formatUsd(12.345), "$12.35");
    assertEqual(formatUsd(1234.5), "$1235");
  });
});
