// web/src/__tests__/intake.test.ts
import { isSafeNonHttpLinkHref } from "../components/timeline/MarkdownInline";
import { extractPortalRouteToken } from "../services/intake-api";
import {
  isIntakeTaskExpanded,
  toggleIntakeTaskExpanded,
} from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("QA intake portal route & store helpers", () => {
  it("extracts valid hqa_ portal tokens and rejects desktop or malformed paths", () => {
    const validTok = "hqa_" + "a1b2c3d4".repeat(8);
    assertEqual(extractPortalRouteToken(`/qa/${validTok}`), validTok);
    assertEqual(extractPortalRouteToken(`/qa/${validTok}/`), validTok);
    assertEqual(extractPortalRouteToken("/"), null);
    assertEqual(extractPortalRouteToken("/qa/"), null);
    assertEqual(extractPortalRouteToken("/qa/short"), null);
    assertEqual(extractPortalRouteToken(`/qa/${validTok}/extra`), null);
    assertEqual(extractPortalRouteToken(`/qa/${validTok.toUpperCase()}`), null);
  });

  it("defaults staged and reopened tasks to expanded and remembers manual toggles", () => {
    assertTrue(isIntakeTaskExpanded("tsk_staged_1", "staged"));
    assertTrue(isIntakeTaskExpanded("tsk_reopen_1", "reopened"));
    assertEqual(isIntakeTaskExpanded("tsk_done_1", "resolved"), false);

    toggleIntakeTaskExpanded("tsk_staged_1", "staged");
    assertEqual(isIntakeTaskExpanded("tsk_staged_1", "staged"), false);

    toggleIntakeTaskExpanded("tsk_done_1", "resolved");
    assertTrue(isIntakeTaskExpanded("tsk_done_1", "resolved"));
  });

  it("blocks unsafe markdown link schemes while allowing anchors and mailto/tel", () => {
    assertTrue(isSafeNonHttpLinkHref("#section-1"));
    assertTrue(isSafeNonHttpLinkHref("mailto:qa@example.com"));
    assertTrue(isSafeNonHttpLinkHref("tel:+8613800138000"));
    assertEqual(isSafeNonHttpLinkHref("#"), false);
    assertEqual(isSafeNonHttpLinkHref(""), false);
    assertEqual(isSafeNonHttpLinkHref("javascript:alert(1)"), false);
    assertEqual(isSafeNonHttpLinkHref("JavaScript:prompt(1)"), false);
    assertEqual(isSafeNonHttpLinkHref("vbscript:msgbox(1)"), false);
    assertEqual(isSafeNonHttpLinkHref("data:text/html,<script>alert(1)</script>"), false);
  });
});
