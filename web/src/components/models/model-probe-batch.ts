// web/src/components/models/model-probe-batch.ts
import type { Setter } from "solid-js";
import type { CustomProviderModelDraft } from "../../store/workspace";

export type ModelProbeFn = (
  modelID: string
) => Promise<{ ok: boolean; latencyMs?: number; error?: string }> | void;

const VERIFY_CONCURRENCY = 4;

/** Probes model ids with bounded concurrency so a batch never floods the endpoint. */
export async function verifyBatch(
  ids: string[],
  probe: ModelProbeFn
): Promise<Record<string, { ok: boolean; error?: string }>> {
  const results: Record<string, { ok: boolean; error?: string }> = {};
  let cursor = 0;
  const workers = Array.from({ length: Math.min(VERIFY_CONCURRENCY, ids.length) }, async () => {
    while (cursor < ids.length) {
      const id = ids[cursor++];
      try {
        const res = await probe(id);
        results[id] = { ok: res ? Boolean(res.ok) : true, error: res?.error };
      } catch (err: any) {
        results[id] = { ok: false, error: err?.message || String(err) };
      }
    }
  });
  await Promise.all(workers);
  return results;
}

export function appendModels(
  setModels: Setter<CustomProviderModelDraft[]>,
  drafts: CustomProviderModelDraft[]
) {
  setModels((prev) => {
    const seen = new Set(prev.map((m) => m.id.trim()).filter(Boolean));
    const next = prev.filter((m) => m.id.trim().length > 0);
    for (const draft of drafts) {
      const id = draft.id.trim();
      if (!id || seen.has(id)) continue;
      seen.add(id);
      next.push({ ...draft, id });
    }
    return next;
  });
}
