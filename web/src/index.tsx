import { render } from "solid-js/web";
import App from "./App";
import {
  applyThemeClass,
  hydrateCompanionFromStorage,
  hydrateWallpaperFromStorage,
  syncSettingsSideEffects,
  workspace,
} from "./store/workspace";
import "./styles.css";
import "./themes-acg.css";
import "./motion-icons.css";
import "./transitions.css";

// Reflect the persisted/detected theme, font scale, wallpaper, and companion standee before first paint.
syncSettingsSideEffects();
applyThemeClass(workspace.theme);
void hydrateWallpaperFromStorage();
void hydrateCompanionFromStorage();

const root = document.getElementById("root");
if (root) {
  render(() => <App />, root);
}
