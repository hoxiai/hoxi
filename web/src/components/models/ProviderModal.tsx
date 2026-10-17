// web/src/components/models/ProviderModal.tsx
import { Show } from "solid-js";
import { Cpu, KeyRound, Plus, ShieldCheck, SlidersHorizontal, X } from "lucide-solid";
import {
  openProviderModal,
  setWorkspace,
  workspace,
  type ProviderModalTab,
} from "../../store/workspace";
import { t } from "../../i18n";
import AgentPolicyMcpTab from "./AgentPolicyMcpTab";
import CustomEndpointTab from "./CustomEndpointTab";
import ModelsRoleTab from "./ModelsRoleTab";
import ProviderCatalogTab from "./ProviderCatalogTab";

export default function ProviderModal() {
  const activeTab = () => workspace.providerModalTab;
  const switchTab = (tab: ProviderModalTab) => {
    if (tab !== "custom") {
      setWorkspace("editingProviderID", null);
    }
    setWorkspace("providerModalTab", tab);
  };

  return (
    <Show when={workspace.providerModalOpen}>
      <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs p-4">
        <div
          class="flex h-[620px] w-full max-w-[860px] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Modal Header */}
          <div class="flex items-center justify-between border-b border-[var(--border-subtle)] px-5 py-3.5">
            <div class="flex items-center gap-3 min-w-0">
              <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                <KeyRound size={16} />
              </div>
              <div class="min-w-0">
                <h2 class="truncate text-[14px] font-semibold text-[var(--text-primary)]">
                  {t("models.modalTitle")}
                </h2>
                <p class="truncate text-[11.5px] text-[var(--text-muted)]">
                  {t("models.modalSubtitle")}
                </p>
              </div>
            </div>

            <div class="flex items-center gap-2 shrink-0">
              <div class="inline-flex rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
                <button
                  type="button"
                  class={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                    activeTab() === "providers"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  onClick={() => switchTab("providers")}
                >
                  <Cpu size={12} />
                  {t("models.tabCatalog")}
                </button>
                <button
                  type="button"
                  class={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                    activeTab() === "custom"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  onClick={() => openProviderModal("custom", workspace.editingProviderID)}
                >
                  <Plus size={12} />
                  {t("models.tabCustom")}
                </button>
                <button
                  type="button"
                  class={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                    activeTab() === "models"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  onClick={() => switchTab("models")}
                >
                  <SlidersHorizontal size={12} />
                  {t("models.tabModels")}
                </button>
                <button
                  type="button"
                  class={`inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[11.5px] font-medium transition-colors cursor-pointer ${
                    activeTab() === "policy"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                  onClick={() => switchTab("policy")}
                >
                  <ShieldCheck size={12} />
                  {t("models.tabPolicy")}
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
          <Show when={activeTab() === "providers"}>
            <ProviderCatalogTab />
          </Show>

          <Show when={activeTab() === "custom"}>
            <CustomEndpointTab />
          </Show>

          <Show when={activeTab() === "models"}>
            <ModelsRoleTab />
          </Show>

          <Show when={activeTab() === "policy"}>
            <AgentPolicyMcpTab />
          </Show>
        </div>
      </div>
    </Show>
  );
}
