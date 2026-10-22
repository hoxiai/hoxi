// web/src/store/git-attribution.ts
// Desktop preference for the `Co-authored-by: Hoxi` commit trailer. The Go
// worktree commit paths read the same preference; this store mirrors it for the
// settings toggle and for agent-driven commit prompts.
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { gitAttributionAPI } from "../services/git-attribution";

export const [gitAttribution, setGitAttribution] = createStore({
  /** Attribution defaults on, matching the backend when no preference is stored. */
  enabled: true,
  loaded: false,
  saving: false,
  error: "",
});

let loading: Promise<void> | undefined;
let gitAttributionGen = 0;

export function loadGitAttribution(): Promise<void> {
  if (loading) return loading;
  const gen = ++gitAttributionGen;
  loading = (async () => {
    try {
      const settings = await gitAttributionAPI.get();
      if (gen !== gitAttributionGen) return;
      setGitAttribution({ enabled: settings.enabled !== false, loaded: true, error: "" });
    } catch {
      if (gen !== gitAttributionGen) return;
      setGitAttribution({ loaded: true, error: t("settings.gitAttributionLoadError") });
    } finally {
      loading = undefined;
    }
  })();
  return loading;
}

export async function saveGitAttribution(enabled: boolean): Promise<void> {
  if (gitAttribution.saving) return;
  const gen = ++gitAttributionGen;
  setGitAttribution({ saving: true, error: "" });
  try {
    const settings = await gitAttributionAPI.put(enabled);
    if (gen === gitAttributionGen) {
      setGitAttribution({ enabled: settings.enabled !== false, loaded: true, error: "" });
    }
  } catch {
    if (gen === gitAttributionGen) {
      setGitAttribution("error", t("settings.gitAttributionSaveError"));
    }
  } finally {
    setGitAttribution("saving", false);
  }
}
