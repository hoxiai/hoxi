import { lazy } from "solid-js";

export default lazy(() =>
  import("./pierre-diff-parts").then((module) => ({ default: module.FileDiffBlock })),
);
