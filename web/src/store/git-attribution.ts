// web/src/store/git-attribution.ts
// Desktop preference for the `Co-authored-by: Hoxi` commit trailer. The Go
// worktree commit paths read the same preference; this store mirrors it for the
// settings toggle and for agent-driven commit prompts.
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { gitAttributionAPI, type GitAttributionMode } from "../services/git-attribution";

export const [gitAttribution, setGitAttribution] = createStore({
  /**
   * `auto` matches the backend default: only Hoxi session worktrees get the
   * trailer, so commits made in the user's own repository stay untouched.
   */
  mode: "auto" as GitAttributionMode,
  loaded: false,
  saving: false,
  error: "",
});

let loading: Promise<void> | undefined;
let gitAttributionGen = 0;

/** Attribution applies to the current commit context. */
export function gitAttributionEnabled(): boolean {
  return gitAttribution.mode !== "never";
}

/**
 * Whether a commit in `directory` carries the trailer. Mirrors
 * worktree.AttributionEnabledFor: `auto` opts in only inside Hoxi session
 * worktrees (`<project>/.hoxi/worktrees/<session-id>`, nested repos included).
 */
export function gitAttributionEnabledFor(directory?: string | null): boolean {
  if (gitAttribution.mode === "always") return true;
  if (gitAttribution.mode === "never") return false;
  const norm = (directory || "").replace(/\\/g, "/");
  return /\/\.hoxi\/worktrees\/[^/]+(\/|$)/.test(norm);
}

export function loadGitAttribution(): Promise<void> {
  if (loading) return loading;
  const gen = ++gitAttributionGen;
  loading = (async () => {
    try {
      const settings = await gitAttributionAPI.get();
      if (gen !== gitAttributionGen) return;
      setGitAttribution({ mode: settings.mode || "auto", loaded: true, error: "" });
    } catch {
      if (gen !== gitAttributionGen) return;
      setGitAttribution({ loaded: true, error: t("settings.gitAttributionLoadError") });
    } finally {
      loading = undefined;
    }
  })();
  return loading;
}

export async function saveGitAttribution(mode: GitAttributionMode): Promise<void> {
  if (gitAttribution.saving) return;
  const gen = ++gitAttributionGen;
  setGitAttribution({ saving: true, error: "" });
  try {
    const settings = await gitAttributionAPI.put(mode);
    if (gen === gitAttributionGen) {
      setGitAttribution({ mode: settings.mode || "auto", loaded: true, error: "" });
    }
  } catch {
    if (gen === gitAttributionGen) {
      setGitAttribution("error", t("settings.gitAttributionSaveError"));
    }
  } finally {
    setGitAttribution("saving", false);
  }
}
