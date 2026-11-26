// web/src/__tests__/intake.test.ts
import { isSafeNonHttpLinkHref } from "../components/timeline/MarkdownInline";
import {
  buildPortalConversationEntries,
  formatPortalDuration,
  groupSanitizedTurns,
  selectMatchingPortalTimeline,
  summarizeTimelineDiff,
} from "../components/tasks/portal-timeline-helpers";
import {
  extractPortalRouteToken,
  type IntakeTaskComment,
  type SanitizedExecutionTurn,
} from "../services/intake-api";
import {
  isIntakeTaskExpanded,
  parseScopedPathInput,
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

  it("folds consecutive tool/todo steps into activity groups and aggregates +/- diff counts", () => {
    const turns: SanitizedExecutionTurn[] = [
      {
        id: "t1",
        createdAt: 1000,
        steps: [
          { id: "s1", kind: "tool", category: "read", label: "Read Login.tsx", status: "completed", startTime: 1000, endTime: 1200 },
          { id: "s2", kind: "todo", label: "Plan", todos: [{ content: "Inspect bug", status: "in_progress" }] },
          {
            id: "s3",
            kind: "tool",
            category: "edit",
            label: "Edit Login.tsx",
            status: "completed",
            additions: 10,
            deletions: 3,
            files: [{ file: "Login.tsx", additions: 10, deletions: 3 }],
            startTime: 1200,
            endTime: 2500,
          },
          { id: "s4", kind: "todo", label: "Plan", todos: [{ content: "Inspect bug", status: "completed" }, { content: "Verify", status: "completed" }] },
        ],
      },
      {
        id: "t2",
        createdAt: 2600,
        steps: [
          {
            id: "s5",
            kind: "tool",
            category: "edit",
            label: "Edit Login.tsx + auth.ts",
            status: "completed",
            additions: 7,
            deletions: 2,
            files: [
              { file: "Login.tsx", additions: 4, deletions: 1 },
              { file: "auth.ts", additions: 3, deletions: 1 },
            ],
            startTime: 2600,
            endTime: 3400,
          },
          {
            id: "s6",
            kind: "tool",
            category: "edit",
            label: "Edit Broken.tsx",
            status: "error",
            additions: 99,
            deletions: 50,
            files: [{ file: "Broken.tsx", additions: 99, deletions: 50 }],
          },
          { id: "s7", kind: "text", text: "Root cause fixed. Ready for retest." },
        ],
      },
    ];

    const segments = groupSanitizedTurns(turns);
    assertEqual(segments.length, 2);
    assertEqual(segments[0].kind, "activity");
    assertEqual(segments[1].kind, "text");
    if (segments[0].kind === "activity") {
      assertEqual(segments[0].toolCount, 4);
      assertEqual(segments[0].latestTodos.length, 2);
      assertEqual(segments[0].hasError, true);
      assertEqual(segments[0].diff.additions, 17);
      assertEqual(segments[0].diff.deletions, 5);
      assertEqual(segments[0].diff.files.length, 2);
      assertEqual(segments[0].diff.files[0].file, "Login.tsx");
      assertEqual(segments[0].diff.files[0].additions, 14);
      assertEqual(segments[0].diff.files[0].deletions, 4);
      assertEqual(segments[0].diff.files[1].file, "auth.ts");
      assertEqual(segments[0].diff.files[1].additions, 3);
    }

    const total = summarizeTimelineDiff(turns);
    assertEqual(total.additions, 17);
    assertEqual(total.deletions, 5);
    assertEqual(total.files.length, 2);
    assertEqual(formatPortalDuration(1000, 3400), "2.4s");
    assertEqual(formatPortalDuration(0, 65000), "");
    assertEqual(formatPortalDuration(1000, 76000), "1m 15s");
  });

  it("interleaves execution turns and follow-up comments chronologically", () => {
    const turns: SanitizedExecutionTurn[] = [
      { id: "turn_1", createdAt: 2000, steps: [{ id: "p1", kind: "text", text: "Round 1 fix" }] },
      { id: "turn_2", createdAt: 2500, steps: [{ id: "p2", kind: "text", text: "Round 1 summary" }] },
      { id: "turn_3", createdAt: 5000, steps: [{ id: "p3", kind: "text", text: "Round 2 fix" }] },
    ];
    const comments: IntakeTaskComment[] = [
      { id: "c0", authorRole: "tester", action: "submit", text: "initial", createdAt: 1000 },
      { id: "c1", authorRole: "developer", action: "awaiting_retest", text: "Please retest", createdAt: 3000 },
      { id: "c2", authorRole: "tester", action: "reopen", text: "Still fails on mobile", createdAt: 4000 },
      { id: "c3", authorRole: "tester", action: "pass", text: "Looks good", createdAt: 6000 },
    ];

    const entries = buildPortalConversationEntries(turns, comments);
    assertEqual(entries.length, 5);
    assertEqual(entries[0].kind, "execution");
    if (entries[0].kind === "execution") assertEqual(entries[0].turns.length, 2);
    assertEqual(entries[1].kind, "comment");
    if (entries[1].kind === "comment") assertEqual(entries[1].comment.id, "c1");
    assertEqual(entries[2].kind, "comment");
    if (entries[2].kind === "comment") assertEqual(entries[2].comment.id, "c2");
    assertEqual(entries[3].kind, "execution");
    if (entries[3].kind === "execution") assertEqual(entries[3].turns.length, 1);
    assertEqual(entries[4].kind, "comment");
    if (entries[4].kind === "comment") assertEqual(entries[4].comment.id, "c3");

    const tlA = { taskId: "tsk_a", busy: false, turns };
    assertEqual(selectMatchingPortalTimeline("tsk_a", tlA), tlA);
    assertEqual(selectMatchingPortalTimeline("tsk_b", tlA), null);
    assertEqual(selectMatchingPortalTimeline(null, tlA), null);
  });

  it("splits consecutive tool groups cleanly across developer continuation notices", () => {
    const turns: SanitizedExecutionTurn[] = [
      {
        id: "a1",
        createdAt: 1000,
        steps: [
          { id: "s1", kind: "tool", category: "read", label: "Read Export.tsx", status: "completed" },
        ],
      },
      {
        id: "u2",
        createdAt: 2000,
        steps: [
          { id: "u2_notice", kind: "notice", category: "developer_followup" },
        ],
      },
      {
        id: "a2",
        createdAt: 2100,
        steps: [
          { id: "s2", kind: "tool", category: "edit", label: "Edit Export.tsx", status: "completed" },
          { id: "s3", kind: "text", text: "Continued fix complete." },
        ],
      },
    ];

    const segments = groupSanitizedTurns(turns);
    assertEqual(segments.length, 4);
    assertEqual(segments[0].kind, "activity");
    assertEqual(segments[1].kind, "notice");
    assertEqual(segments[2].kind, "activity");
    assertEqual(segments[3].kind, "text");
    if (segments[0].kind === "activity") assertEqual(segments[0].toolCount, 1);
    if (segments[1].kind === "notice") assertEqual(segments[1].category, "developer_followup");
    if (segments[2].kind === "activity") assertEqual(segments[2].toolCount, 1);
  });

  it("parses comma, CJK delimiter, and newline scoped path lists without duplicates", () => {
    assertEqual(
      parseScopedPathInput("internal/auth, config，migrations、internal/auth\nweb/src/policy; ").join(","),
      "internal/auth,config,migrations,web/src/policy"
    );
    assertEqual(parseScopedPathInput("   \n , ").length, 0);
  });
});
