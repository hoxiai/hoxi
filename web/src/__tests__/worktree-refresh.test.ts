import { hoxiAPI } from "../services/api";
import { cancelScheduledWorktreeStatus, scheduleRefreshWorktreeStatus } from "../store/workspace-sessions";
import { assertEqual, describe, it } from "./test-harness";

describe("worktree refresh lifecycle", () => {
  it("does not request status after the scheduled refresh is cancelled", async () => {
    const original = hoxiAPI.getWorktreeStatus;
    let calls = 0;
    hoxiAPI.getWorktreeStatus = async () => { calls++; throw new Error("cancelled refresh reached transport"); };
    try {
      scheduleRefreshWorktreeStatus("ses_cancelled_refresh", 1);
      cancelScheduledWorktreeStatus("ses_cancelled_refresh");
      await new Promise((resolve) => setTimeout(resolve, 15));
      assertEqual(calls, 0);
    } finally {
      cancelScheduledWorktreeStatus("ses_cancelled_refresh");
      hoxiAPI.getWorktreeStatus = original;
    }
  });
});
