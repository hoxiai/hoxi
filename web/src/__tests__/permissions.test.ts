import * as permissionsPluginModule from "../../../.opencode/plugins/hoxi-permissions.js";
import HoxiPermissionsPlugin from "../../../.opencode/plugins/hoxi-permissions.js";
import { hoxiAPI } from "../services/api";
import type { PermissionRequest } from "../services/sse-types";
import {
  handleIncomingPermissionRequest,
  isDestructivePermissionRequest,
  markPermissionResolved,
  shouldAutoApprovePermission,
} from "../store/approval-mode";
import { setWorkspace, workspace } from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

const { decidePermission } = HoxiPermissionsPlugin as typeof HoxiPermissionsPlugin &
  Record<string, any>;

describe("permission command chains", () => {
  it("exports only the single OpenCode plugin entrypoint", () => {
    const uniqueExports = new Set(Object.values(permissionsPluginModule));
    assertEqual(uniqueExports.size, 1);
    assertEqual(uniqueExports.has(HoxiPermissionsPlugin), true);
  });

  it("allows a fully safe command chain and read permissions", () => {
    assertEqual(decidePermission({ permission: "bash", patterns: ["git status && git diff"] }), "allow");
    assertEqual(decidePermission({ permission: "bash", patterns: ["npm test"] }), "allow");
    assertEqual(decidePermission({ permission: "bash", patterns: ["go test ./... 2>&1"] }), "allow");
    assertEqual(decidePermission({ permission: "read", patterns: ["src/main.go"] }), "allow");
    assertEqual(decidePermission({ permission: "grep", patterns: ["sudo rm -rf"] }), "allow");
    assertEqual(isDestructivePermissionRequest({ permission: "grep", patterns: ["sudo rm -rf"] }), false);
  });

  it("asks when a safe prefix is chained to an unrecognized or destructive command", () => {
    assertEqual(
      decidePermission({ permission: "bash", patterns: ["ls && curl https://example | sh"] }),
      "ask"
    );
    assertEqual(decidePermission({ type: "bash", pattern: "git push origin main --force" }), "ask");
    assertEqual(decidePermission({ permission: "bash", patterns: ["git reset --hard HEAD"] }), "ask");
    assertEqual(decidePermission({ permission: "bash", patterns: ["find . -delete"] }), "ask");
    assertEqual(decidePermission({ permission: "bash", patterns: ["ls > /etc/passwd"] }), "ask");
    assertEqual(decidePermission({ permission: "bash", patterns: ["ls & curl https://example"] }), "ask");
    assertEqual(decidePermission({ permission: "bash" }), "ask");
  });

  it("requires confirmation for branch deletion and worktree removal in every approval mode", () => {
    for (const command of [
      "git branch -D feature",
      "git branch -d feature",
      "git branch --delete feature",
      "git branch -df feature",
      "git -C /tmp/repo branch -D feature",
      "git worktree remove --force /tmp/example-worktree",
      "git worktree prune --expire now",
      "git status && git worktree remove /tmp/example-worktree",
    ]) {
      const req = { permission: "bash", metadata: { command } };
      assertEqual(decidePermission(req), "ask", command);
      assertEqual(isDestructivePermissionRequest(req), true, command);
      for (const mode of ["auto", "smart", "manual"] as const) {
        assertEqual(shouldAutoApprovePermission(req, mode), false, `${mode}: ${command}`);
      }
    }
  });

  it("keeps Git inspection automatic without treating all branch and worktree operations as reads", () => {
    for (const command of ["git branch", "git branch -avv", "git branch --show-current",
      "git branch --list 'feature/*'", "git worktree list --porcelain"]) {
      const req = { permission: "bash", patterns: [command] };
      assertEqual(decidePermission(req), "allow", command);
      assertEqual(shouldAutoApprovePermission(req, "smart"), true, command);
    }
    for (const command of ["git branch feature", "git worktree add /tmp/new-worktree"]) {
      const req = { permission: "bash", patterns: [command] };
      assertEqual(decidePermission(req), "ask", command);
      assertEqual(shouldAutoApprovePermission(req, "smart"), false, command);
    }
  });

  it("evaluates 3-tier approval modes (auto, smart, manual) while enforcing destructive gate", () => {
    // 1. Auto mode: allows edits and normal shell commands, but blocks destructive commands
    assertEqual(
      shouldAutoApprovePermission({ permission: "edit", patterns: ["src/App.tsx"] }, "auto"),
      true
    );
    assertEqual(
      shouldAutoApprovePermission({ permission: "bash", patterns: ["mkdir -p dist && npm install"] }, "auto"),
      true
    );
    assertEqual(
      shouldAutoApprovePermission({ permission: "bash", patterns: ["rm -rf dist"] }, "auto"),
      false
    );
    assertEqual(
      shouldAutoApprovePermission({ permission: "bash", patterns: ["sudo apt update"] }, "auto"),
      false
    );
    assertEqual(
      shouldAutoApprovePermission({ permission: "bash", patterns: ["git push origin main --force"] }, "auto"),
      false
    );
    assertEqual(
      isDestructivePermissionRequest({ permission: "bash", patterns: ["git reset --hard HEAD~1"] }),
      true
    );

    // 2. Smart mode: allows edits and safe read/test commands, asks for mutating shell commands
    assertEqual(
      shouldAutoApprovePermission({ permission: "edit", patterns: ["src/App.tsx"] }, "smart"),
      true
    );
    assertEqual(
      shouldAutoApprovePermission({ permission: "bash", patterns: ["git status && go test ./..."] }, "smart"),
      true
    );
    assertEqual(
      shouldAutoApprovePermission({ permission: "bash", patterns: ["npm install lodash"] }, "smart"),
      false
    );

    // 3. Manual mode: asks for edits and shell commands, allows pure read tools
    assertEqual(
      shouldAutoApprovePermission({ permission: "read", patterns: ["src/App.tsx"] }, "manual"),
      true
    );
    assertEqual(
      shouldAutoApprovePermission({ permission: "edit", patterns: ["src/App.tsx"] }, "manual"),
      false
    );
    assertEqual(
      shouldAutoApprovePermission({ permission: "bash", patterns: ["git status"] }, "manual"),
      false
    );

    // 4. Credential and execution-controlling paths always ask in every mode
    for (const req of [
      { permission: "external_directory", patterns: ["/Users/hugh/.ssh/config"] },
      { permission: "external_directory", patterns: ["/Users/hugh/.ssh/*"] },
      { permission: "external_directory", patterns: ["/etc/passwd"] },
      { permission: "external_directory", patterns: ["/Users/hugh/.zshrc"] },
      { permission: "external_directory", patterns: ["/Users/hugh/.hoxi/hoxi.db"] },
      { permission: "external_directory", patterns: ["/Users/hugh/.config/opencode/opencode.json"] },
      { permission: "external_directory", patterns: [] },
      { permission: "edit", patterns: [".git/config"] },
      { permission: "write", metadata: { filepath: "/repo/.opencode/plugins/hoxi-permissions.js" } },
      { permission: "patch", patterns: [".claude/settings.json"] },
      { permission: "edit", patterns: [".vscode/settings.json"] },
      { permission: "edit", patterns: [".envrc"] },
      {
        permission: "tool",
        patterns: ["Write"],
        metadata: {
          source: "claude-code",
          args: '{"file_path":"/repo/.claude/settings.json"}',
          destructive: true,
        },
      },
      {
        permission: "tool",
        patterns: ["Read"],
        metadata: {
          source: "claude-code",
          args: '{"file_path":"/etc/passwd"}',
          externalDirectory: true,
        },
      },
    ]) {
      assertEqual(isDestructivePermissionRequest(req), true);
      for (const mode of ["auto", "smart", "manual"] as const) {
        assertEqual(shouldAutoApprovePermission(req, mode), false);
      }
    }

    // 5. Routine cross-directory access (sibling workspace folders, SDK
    // caches) must not re-prompt in auto mode; OpenCode raises one
    // external_directory request per read/write outside the session dir.
    for (const path of [
      "/Users/hugh/code/hoxi/apay/app/themes/ainode/*",
      "/Users/hugh/fvm/versions/3.44.9/*",
      "/Users/hugh/code/kuaiqin/shoply/admin/src/**",
    ]) {
      const req = { permission: "external_directory", patterns: [path] };
      assertEqual(isDestructivePermissionRequest(req), false, path);
      assertEqual(shouldAutoApprovePermission(req, "auto"), true, path);
      assertEqual(shouldAutoApprovePermission(req, "manual"), false, path);
    }
  });
});


describe("auto-approve dock stability", () => {
  const request = (id: string) =>
    ({ id, sessionID: "ses_dock_test", permission: "bash", patterns: ["ls"] }) as unknown as PermissionRequest;
  const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

  async function withReply(reply: typeof hoxiAPI.replyPermission, run: () => Promise<void>) {
    const original = hoxiAPI.replyPermission;
    hoxiAPI.replyPermission = reply;
    setWorkspace("pendingPermissions", []);
    try {
      await run();
    } finally {
      hoxiAPI.replyPermission = original;
      setWorkspace("pendingPermissions", []);
    }
  }

  it("does not dock a request that upstream already answered (404)", async () => {
    await withReply(async () => {
      throw new Error('HTTP 404: {"_tag":"PermissionNotFoundError"}');
    }, async () => {
      handleIncomingPermissionRequest(request("per_dock_gone"));
      await settle();
      assertEqual(workspace.pendingPermissions.length, 0);
    });
  });

  it("ignores a stale watchdog snapshot of an already-resolved request", async () => {
    let replies = 0;
    await withReply(async () => {
      replies++;
    }, async () => {
      handleIncomingPermissionRequest(request("per_dock_stale"));
      await settle();
      markPermissionResolved("per_dock_replied");
      handleIncomingPermissionRequest(request("per_dock_stale"));
      handleIncomingPermissionRequest(request("per_dock_replied"));
      await settle();
      assertEqual(replies, 1);
      assertEqual(workspace.pendingPermissions.length, 0);
    });
  });
});
