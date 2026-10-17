import { render } from "solid-js/web";
import App from "./App";
import { applyThemeClass, workspace } from "./store/workspace";
import "./styles.css";

// Light is the default; reflect the persisted/detected theme before first paint.
applyThemeClass(workspace.theme);

const root = document.getElementById("root");
if (root) {
  render(() => <App />, root);
}
