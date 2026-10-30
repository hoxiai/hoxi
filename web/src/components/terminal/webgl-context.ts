// web/src/components/terminal/webgl-context.ts
// xterm's WebGL addon never calls loseContext() on dispose, so each disposed
// terminal keeps its drawing buffers and glyph atlas on the GPU until garbage
// collection. The terminal is rebuilt on every session switch, so contexts piled
// up (WKWebView reached GBs of graphics memory) until the 16-context cap
// force-dropped the oldest one.

/**
 * Runs `load` and returns a release function for the WebGL contexts on canvases
 * it added under `root`. Canvases that already existed are left alone, and 2D
 * canvases return null for a "webgl2" query, so nothing new is created here.
 */
export function trackWebglContexts(root: ParentNode, load: () => void): () => void {
  const before = new Set(Array.from(root.querySelectorAll("canvas")));
  load();
  const contexts: WebGL2RenderingContext[] = [];
  for (const canvas of Array.from(root.querySelectorAll("canvas"))) {
    if (before.has(canvas)) continue;
    const gl = canvas.getContext("webgl2");
    if (gl) contexts.push(gl);
  }
  let released = false;
  return () => {
    if (released) return;
    released = true;
    for (const gl of contexts) {
      if (!gl.isContextLost()) gl.getExtension("WEBGL_lose_context")?.loseContext();
    }
  };
}
