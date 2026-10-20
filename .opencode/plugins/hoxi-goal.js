// .opencode/plugins/hoxi-goal.js
// Registers goal_report for Hoxi session goals. The agent reports "complete" or
// "blocked" through this tool; the Hoxi web app reads the recorded tool call and
// updates the goal (see web/src/store/session-goal.ts).
import { tool } from "@opencode-ai/plugin";

export const HoxiGoalPlugin = async () => {
  return {
    tool: {
      goal_report: tool({
        description:
          "Report the outcome of the active session goal. Use status \"complete\" only when the objective is fully met and verified. Use status \"blocked\" with a concrete reason when you cannot make safe progress without the user.",
        args: {
          status: tool.schema
            .enum(["complete", "blocked"])
            .describe("\"complete\" when the objective is met, \"blocked\" when a real blocker prevents safe progress"),
          reason: tool.schema
            .string()
            .max(2000)
            .describe("One or two sentences: what was verified, or what blocks progress"),
        },
        async execute(args) {
          return `Goal ${args.status} recorded: ${args.reason}`;
        },
      }),
    },
  };
};

export default HoxiGoalPlugin;
