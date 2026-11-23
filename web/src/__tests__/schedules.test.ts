// web/src/__tests__/schedules.test.ts
import {
  formatScheduleTrigger,
  scheduleNeedsAttention,
} from "../components/schedules/schedule-helpers";
import { isDestructivePermissionRequest } from "../policy/permission-policy.js";
import { explainPermissionRequest } from "../policy/approval-reason.js";
import type { ScheduleItem } from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("scheduled tasks helpers & approval policy mirror", () => {
  it("formats cron and interval triggers concisely", () => {
    assertEqual(formatScheduleTrigger({ kind: "cron", cron: "0 9 * * 1-5" }), "0 9 * * 1-5");
    assertEqual(formatScheduleTrigger({ kind: "interval", intervalMinutes: 30 }), "30m");
    assertEqual(formatScheduleTrigger({ kind: "interval", intervalMinutes: 120 }), "2h");
  });

  it("identifies schedules requiring user attention", () => {
    const base: ScheduleItem = {
      id: "sch_1",
      name: "PR Check",
      enabled: true,
      directory: "/repo",
      trigger: { kind: "cron", cron: "0 9 * * *" },
      execution: { mode: "plan", sandboxMode: "local" },
      prompt: "Check PRs",
      injectLastSummary: true,
      notifyOn: "alert_or_error",
      maxConsecutiveFails: 3,
      createdAt: 1,
      updatedAt: 1,
    };
    assertEqual(scheduleNeedsAttention({ ...base, lastOutcome: "ok", alertUnseen: false }), false);
    assertTrue(scheduleNeedsAttention({ ...base, lastOutcome: "alert", alertUnseen: true }));
    assertTrue(scheduleNeedsAttention({ ...base, lastOutcome: "error", alertUnseen: false }));
    assertTrue(scheduleNeedsAttention({ ...base, lastOutcome: "blocked", alertUnseen: false }));
  });

  it("treats hoxi_schedule list as routine and mutations as destructive requiring desktop approval", () => {
    assertEqual(
      isDestructivePermissionRequest({
        permission: "tool",
        patterns: ["hoxi_schedule"],
        metadata: { tool: "hoxi_schedule", args: JSON.stringify({ action: "list" }) },
      }),
      false
    );
    assertTrue(
      isDestructivePermissionRequest({
        permission: "hoxi_schedule",
        patterns: ["hoxi_schedule create: Daily Check"],
        metadata: { tool: "hoxi_schedule", action: "create", args: JSON.stringify({ action: "create" }) },
      })
    );
    const reason = explainPermissionRequest({
      permission: "hoxi_schedule",
      patterns: ["hoxi_schedule create: Daily Check"],
      metadata: { tool: "hoxi_schedule", action: "create" },
    });
    assertEqual(reason.severity, "danger");
    assertEqual(reason.detail, "hoxi_schedule");
  });
});
