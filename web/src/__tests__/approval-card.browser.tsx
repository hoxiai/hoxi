import { render } from "solid-js/web";
import ApprovalGateCard from "../components/approval/ApprovalGateCard";
import { t } from "../i18n";
import type { PermissionRequest } from "../services/sse-types";
import "../styles.css";

function check(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

const frame = () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

function request(id: string, command: string, patterns: string[] = [command]): PermissionRequest {
  return {
    id,
    sessionID: "ses_approval_card",
    permission: "bash",
    patterns,
    metadata: { command, source: id.startsWith("per_rnr_") ? "claude-code" : "opencode" },
    always: [command],
  };
}

function buttonLabels(el: HTMLElement): string[] {
  return Array.from(el.querySelectorAll("button")).map((b) => (b.textContent ?? "").trim());
}

/**
 * Real DOM regression; run with `npm run test:approval-card` (Chrome required).
 * The card offers "always" only when the gate keeps the grant: an opaque
 * command shows why it asks every time instead of a button the gate ignores.
 */
export async function runBrowserTest(): Promise<string[]> {
  // This page must never reach a backend.
  window.fetch = async () => new Response("{}", { headers: { "Content-Type": "application/json" } });
  const results: string[] = [];
  const hint = t("approval.sessionGrantUnavailableOpaque");
  const rulesPrefix = t("approval.autoApproveRules", { count: "§" }).split("§")[0].trim();
  const scenarios: Array<{ name: string; req: PermissionRequest; always: string | null; hint: boolean; ruleAllow?: boolean }> = [
    { name: "runner opaque command", req: request("per_rnr_card_opaque", 'bash -c "$X"'), always: null, hint: true },
    {
      name: "runner command full trust reduces",
      req: request("per_rnr_card_inline", "python3 -c 'print(1)'"),
      always: t("approval.allowSessionCommand"),
      hint: false,
    },
    { name: "runner destructive command", req: request("per_rnr_card_rm", "rm -rf build"), always: null, hint: false },
    {
      name: "OpenCode opaque command judged as one command",
      req: request("per_card_oc_opaque", "env | grep -i token", ["env", "grep -i token"]),
      always: null,
      hint: true,
      ruleAllow: false,
    },
    {
      name: "OpenCode plain command",
      req: request("per_card_oc_plain", "git status"),
      always: t("approval.allowAlways"),
      hint: false,
      ruleAllow: true,
    },
  ];
  for (const s of scenarios) {
    const el = document.createElement("div");
    el.dataset.scenario = s.name;
    el.style.width = "560px";
    el.style.margin = "12px";
    document.body.appendChild(el);
    render(() => <ApprovalGateCard request={s.req} />, el);
    await frame();
    const labels = buttonLabels(el);
    check(labels.some((l) => l.includes(t("approval.allowOnce"))), `${s.name}: allow once is missing`);
    const offered = labels.some((l) => l.includes(t("approval.allowSessionCommand")) || l.includes(t("approval.allowAlways")));
    if (s.always) {
      check(labels.some((l) => l.includes(s.always ?? "")), `${s.name}: expected "${s.always}" in ${JSON.stringify(labels)}`);
    } else {
      check(!offered, `${s.name}: a session grant the gate drops is offered: ${JSON.stringify(labels)}`);
    }
    check((el.textContent ?? "").includes(hint) === s.hint, `${s.name}: opaque hint shown=${!s.hint}`);
    if (s.ruleAllow !== undefined) {
      // The rule list's allow also replies "always", so it follows the same gate.
      const toggle = Array.from(el.querySelectorAll("button")).find((b) => (b.textContent ?? "").trim().startsWith(rulesPrefix));
      check(toggle, `${s.name}: rule list toggle is missing`);
      toggle.click();
      await frame();
      const exact = buttonLabels(el);
      check(exact.includes(t("approval.ruleAlwaysDeny")), `${s.name}: deny rule is missing`);
      check(exact.includes(t("approval.ruleAlwaysAllow")) === s.ruleAllow, `${s.name}: allow rule shown=${!s.ruleAllow}`);
    }
    results.push(s.always ? `${s.name} offers "${s.always}"` : `${s.name} offers no session grant${s.hint ? " and says why" : ""}`);
  }
  return results;
}
