import { createSignal } from "solid-js";
import { appGrantsAPI, type AppApprovalRequest, type AppConnectRequest, type AppGrant } from "../services/app-grants";
import { t } from "../i18n";

export function createAppGrantSettings() {
  const [grants, setGrants] = createSignal<AppGrant[]>([]);
  const [pending, setPending] = createSignal<AppConnectRequest[]>([]);
  const [approvals, setApprovals] = createSignal<AppApprovalRequest[]>([]);
  const [selected, setSelected] = createSignal<Record<string, string[]>>({});
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  let disposed = false;
  let generation = 0;

  async function refresh() {
    const current = ++generation;
    const results = await Promise.allSettled([appGrantsAPI.list(), appGrantsAPI.pending(), appGrantsAPI.approvals()]);
    if (disposed || generation !== current) return;
    const [grantList, connectList, approvalList] = results;
    if (grantList.status === "fulfilled") setGrants(grantList.value);
    if (connectList.status === "fulfilled") {
      setPending(connectList.value);
      setSelected((old) => Object.fromEntries(connectList.value.map((item) => [item.requestId, old[item.requestId] ?? item.scopes])));
    }
    if (approvalList.status === "fulfilled") setApprovals(approvalList.value);
    setError(results.some((item) => item.status === "rejected") ? t("settings.appGrantLoadError") : "");
  }

  function toggleScope(id: string, scope: string, enabled: boolean) {
    setSelected((old) => ({ ...old, [id]: enabled ? [...(old[id] ?? []), scope] : (old[id] ?? []).filter((item) => item !== scope) }));
  }

  async function act(action: () => Promise<unknown>) {
    if (disposed || busy()) return;
    setBusy(true);
    setError("");
    try {
      await action();
      if (!disposed) await refresh();
    } catch {
      if (!disposed) setError(t("settings.appGrantActionError"));
    } finally {
      if (!disposed) setBusy(false);
    }
  }

  const unsubscribe = appGrantsAPI.onChange(() => { if (!busy()) void refresh(); });
  return {
    grants, pending, approvals, selected, busy, error, refresh, toggleScope,
    decideConnect: (id: string, approve: boolean) => act(async () => {
      const scopes = selected()[id];
      if (approve && !scopes?.length) return;
      await appGrantsAPI.decideConnect(id, approve, scopes);
    }),
    decideApproval: (id: string, approve: boolean) => act(() => appGrantsAPI.decideApproval(id, approve)),
    revoke: (id: string) => act(() => appGrantsAPI.revoke(id)),
    dispose: () => { disposed = true; unsubscribe(); },
  };
}
