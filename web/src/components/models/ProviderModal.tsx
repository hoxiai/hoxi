// web/src/components/models/ProviderModal.tsx
import { createSignal, Show } from "solid-js";
import { KeyRound, Plus, ShieldCheck, X } from "lucide-solid";
import { setWorkspace, workspace } from "../../store/workspace";
import AgentPolicyMcpTab from "./AgentPolicyMcpTab";
import CustomEndpointTab from "./CustomEndpointTab";
import ProviderCatalogTab from "./ProviderCatalogTab";

export default function ProviderModal() {
  const [tab, setTab] = createSignal<"catalog" | "custom" | "agent">("catalog");

  return (
    <Show when={workspace.providerModalOpen}>
      <div
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs p-4"
        onClick={() => setWorkspace("providerModalOpen", false)}
      >
        <div
          class="flex h-[560px] w-full max-w-[780px] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Modal Header */}
          <div class="flex items-center justify-between border-b border-[var(--border-subtle)] px-5 py-3.5">
            <div class="flex items-center gap-3">
              <div class="flex h-8 w-8 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                <KeyRound size={16} />
              </div>
              <div>
                <h2 class="text-[14px] font-semibold text-[var(--text-primary)]">
                  Models, Auto-Approve & MCP
                </h2>
                <p class="text-[11.5px] text-[var(--text-muted)]">
                  Configure AI providers, custom endpoints, auto-approve permissions & MCP tools
                </p>
              </div>
            </div>

            <div class="flex items-center gap-2">
              <div class="inline-flex rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
                <button
                  type="button"
                  class={`rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                    tab() === "catalog"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  onClick={() => setTab("catalog")}
                >
                  Providers
                </button>
                <button
                  type="button"
                  class={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                    tab() === "custom"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  onClick={() => setTab("custom")}
                >
                  <Plus size={12} />
                  Custom Endpoint
                </button>
                <button
                  type="button"
                  class={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                    tab() === "agent"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  onClick={() => setTab("agent")}
                >
                  <ShieldCheck size={12} />
                  Auto-Approve & MCP
                </button>
              </div>

              <button
                type="button"
                class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                onClick={() => setWorkspace("providerModalOpen", false)}
              >
                <X size={16} />
              </button>
            </div>
          </div>

          {/* Modal Body */}
          <Show when={tab() === "catalog"}>
            <ProviderCatalogTab />
          </Show>

          <Show when={tab() === "custom"}>
            <CustomEndpointTab />
          </Show>

          <Show when={tab() === "agent"}>
            <AgentPolicyMcpTab />
          </Show>
        </div>
      </div>
    </Show>
  );
}
