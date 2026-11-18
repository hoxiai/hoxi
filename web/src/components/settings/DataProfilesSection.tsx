// web/src/components/settings/DataProfilesSection.tsx
import { createSignal, For, onMount, Show } from "solid-js";
import { CheckCircle2, Database, Lock, Plus, Terminal, Trash2 } from "lucide-solid";
import { t } from "../../i18n";
import {
  dataProfiles,
  loadDataProfiles,
  removeDataProfile,
  runDataProfileTest,
  upsertDataProfile,
  type DataProfileKind,
  type DataProfileUpsertPayload,
} from "../../store/workspace";
import { SettingGroup } from "./setting-controls";

function parseSSHTarget(raw: string): { user?: string; host: string; port?: number } {
  const trimmed = raw.trim();
  let user: string | undefined;
  let hostPort = trimmed;
  const atIdx = trimmed.indexOf("@");
  if (atIdx > 0) {
    user = trimmed.slice(0, atIdx);
    hostPort = trimmed.slice(atIdx + 1);
  }
  const colonIdx = hostPort.lastIndexOf(":");
  if (colonIdx > 0) {
    const portNum = Number(hostPort.slice(colonIdx + 1));
    if (Number.isFinite(portNum) && portNum > 0) {
      return { user, host: hostPort.slice(0, colonIdx), port: portNum };
    }
  }
  return { user, host: hostPort };
}

export function DataProfilesSection() {
  const [id, setId] = createSignal("");
  const [kind, setKind] = createSignal<DataProfileKind>("postgres");
  const [target, setTarget] = createSignal("");
  const [secret, setSecret] = createSignal("");
  const [readOnly, setReadOnly] = createSignal(true);
  const [busy, setBusy] = createSignal(false);
  const [testingId, setTestingId] = createSignal<string | null>(null);
  const [status, setStatus] = createSignal<{ ok: boolean; text: string } | null>(null);

  onMount(() => {
    void loadDataProfiles().catch(() => {});
  });

  const onSave = async (ev: Event) => {
    ev.preventDefault();
    const cleanId = id().trim().replace(/[^a-zA-Z0-9._-]/g, "-");
    const cleanTarget = target().trim();
    if (!cleanId || !cleanTarget || busy()) return;
    setBusy(true);
    setStatus(null);
    try {
      const k = kind();
      const payload: DataProfileUpsertPayload = {
        id: cleanId,
        kind: k,
        readOnly: readOnly(),
        ...(secret().trim() ? { secret: secret().trim() } : {}),
      };
      if (k === "http_api") {
        payload.baseUrl = cleanTarget;
        payload.authMode = secret().trim() ? "bearer" : "none";
      } else if (k === "sqlite") {
        payload.dbPath = cleanTarget;
      } else if (k === "postgres" || k === "mysql") {
        payload.dsn = cleanTarget;
      } else if (k === "ssh") {
        const parsed = parseSSHTarget(cleanTarget);
        payload.sshHost = parsed.host;
        if (parsed.user) payload.sshUser = parsed.user;
        if (parsed.port) payload.sshPort = parsed.port;
        payload.authMode = secret().trim() ? "private_key" : "agent";
      }
      await upsertDataProfile(payload);
      setId("");
      setTarget("");
      setSecret("");
      setStatus({ ok: true, text: t("settings.dataProfileSaved", { id: cleanId }) });
    } catch (err: any) {
      setStatus({ ok: false, text: err?.message || String(err) });
    } finally {
      setBusy(false);
    }
  };

  const onTest = async (profileId: string) => {
    if (testingId()) return;
    setTestingId(profileId);
    setStatus(null);
    try {
      await runDataProfileTest(profileId);
      setStatus({ ok: true, text: t("settings.dataProfileTestOk", { id: profileId }) });
    } catch (err: any) {
      setStatus({ ok: false, text: err?.message || String(err) });
    } finally {
      setTestingId(null);
    }
  };

  const onDelete = async (profileId: string) => {
    setStatus(null);
    try {
      await removeDataProfile(profileId);
      setStatus({ ok: true, text: t("settings.dataProfileDeleted", { id: profileId }) });
    } catch (err: any) {
      setStatus({ ok: false, text: err?.message || String(err) });
    }
  };

  return (
    <SettingGroup
      title={t("settings.dataProfilesTitle")}
      tip={t("settings.dataProfilesSubtitle")}
    >
      <div class="p-4 space-y-3">
        <form onSubmit={(e) => void onSave(e)} class="space-y-2">
          <div class="flex flex-wrap items-center gap-2">
            <input
              type="text"
              placeholder={t("settings.dataProfileIdPlaceholder")}
              value={id()}
              onInput={(e) => setId(e.currentTarget.value)}
              class="w-40 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            />
            <select
              value={kind()}
              onChange={(e) => setKind(e.currentTarget.value as DataProfileKind)}
              class="rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[12px] text-[var(--text-primary)] outline-none"
            >
              <option value="postgres">PostgreSQL</option>
              <option value="mysql">MySQL</option>
              <option value="sqlite">SQLite</option>
              <option value="ssh">SSH Host</option>
              <option value="http_api">HTTP API</option>
            </select>
            <input
              type="text"
              placeholder={t("settings.dataProfileTargetPlaceholder")}
              value={target()}
              onInput={(e) => setTarget(e.currentTarget.value)}
              class="min-w-[220px] flex-1 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            />
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <input
              type="password"
              placeholder={t("settings.dataProfileSecretPlaceholder")}
              value={secret()}
              onInput={(e) => setSecret(e.currentTarget.value)}
              class="min-w-[220px] flex-1 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-3 py-1.5 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
            />
            <label class="inline-flex items-center gap-1.5 px-2 text-[12px] text-[var(--text-secondary)] cursor-pointer select-none">
              <input
                type="checkbox"
                checked={readOnly()}
                onChange={(e) => setReadOnly(e.currentTarget.checked)}
                class="accent-[var(--accent-primary)]"
              />
              <Lock size={12} class="text-[var(--accent-primary)]" />
              <span>{t("settings.dataProfileReadOnlyLabel")}</span>
            </label>
            <button
              type="submit"
              disabled={busy() || !id().trim() || !target().trim()}
              class="inline-flex items-center gap-1 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] disabled:opacity-40 cursor-pointer"
            >
              <Plus size={13} />
              {t("models.add")}
            </button>
          </div>
        </form>

        <div class="space-y-1.5">
          <Show
            when={dataProfiles().length > 0}
            fallback={
              <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-3 text-[12px] text-[var(--text-muted)]">
                {t("settings.dataProfilesEmpty")}
              </div>
            }
          >
            <For each={dataProfiles()}>
              {(item) => (
                <div class="flex items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3.5 py-2">
                  <div class="flex items-center gap-2 min-w-0">
                    <Show
                      when={item.kind === "ssh"}
                      fallback={<Database size={14} class="text-[var(--accent-primary)] shrink-0" />}
                    >
                      <Terminal size={14} class="text-[var(--accent-primary)] shrink-0" />
                    </Show>
                    <span class="font-mono text-[12px] font-semibold text-[var(--text-primary)]">
                      {item.id}
                    </span>
                    <span class="rounded-md bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10px] uppercase text-[var(--text-secondary)]">
                      {item.kind}
                    </span>
                    <Show when={item.readOnly}>
                      <span class="rounded-md bg-[var(--ok-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--ok-text)]">
                        RO
                      </span>
                    </Show>
                    <span class="truncate font-mono text-[11px] text-[var(--text-muted)]">
                      {item.dsnMasked ||
                        item.dbPath ||
                        item.baseUrl ||
                        (item.sshHost ? `${item.sshUser ? `${item.sshUser}@` : ""}${item.sshHost}:${item.sshPort || 22}` : "")}
                    </span>
                  </div>
                  <div class="flex items-center gap-1.5 shrink-0">
                    <button
                      type="button"
                      disabled={testingId() === item.id}
                      onClick={() => void onTest(item.id)}
                      class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      <CheckCircle2 size={12} />
                      {testingId() === item.id ? t("settings.dataProfileTestingBtn") : t("settings.dataProfileTestBtn")}
                    </button>
                    <button
                      type="button"
                      onClick={() => void onDelete(item.id)}
                      class="text-[var(--text-muted)] hover:text-[var(--danger-text)] p-1 cursor-pointer"
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                </div>
              )}
            </For>
          </Show>
        </div>

        <Show when={status()}>
          <div
            class={`rounded-xl border px-3.5 py-2 text-[12px] ${
              status()!.ok
                ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
            }`}
          >
            {status()!.text}
          </div>
        </Show>
      </div>
    </SettingGroup>
  );
}
