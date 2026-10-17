import { render } from "solid-js/web";
import App from "./App";
import { applyThemeClass, syncSettingsSideEffects, workspace } from "./store/workspace";
import "./styles.css";

// Reflect the persisted/detected theme and font scale before first paint.
syncSettingsSideEffects();
applyThemeClass(workspace.theme);

const root = document.getElementById("root");
if (root) {
  render(() => <App />, root);
}
