// web/src/components/layout/WallpaperBackdrop.tsx
import { createMemo, Show } from "solid-js";
import { resolveActiveWallpaperUrl, wallpaperState } from "../../store/workspace";

export default function WallpaperBackdrop() {
  const activeUrl = createMemo(() => resolveActiveWallpaperUrl());

  return (
    <Show when={wallpaperState.enabled && Boolean(activeUrl())}>
      <div
        aria-hidden="true"
        class="pointer-events-none fixed inset-0 z-0 overflow-hidden select-none"
      >
        <div
          class="absolute inset-0 bg-cover bg-no-repeat transition-[background-image,transform,filter] duration-300"
          style={{
            "background-image": `url("${activeUrl().replace(/"/g, '\\"')}")`,
            "background-position": "72% center",
            filter:
              "contrast(var(--wallpaper-img-contrast, 0.88)) brightness(var(--wallpaper-img-brightness, 1)) saturate(1.04)",
          }}
        />
        <div
          class="absolute inset-0 bg-[var(--bg-canvas)] transition-opacity duration-200"
          style={{
            opacity: "var(--wallpaper-dim-opacity, 0.22)",
          }}
        />
        <div
          class="absolute inset-x-0 top-0 h-20 bg-gradient-to-b from-[var(--bg-canvas)] via-[var(--bg-canvas)]/65 to-transparent transition-opacity duration-200"
          style={{
            opacity: "var(--wallpaper-header-scrim, 0.56)",
          }}
        />
      </div>
    </Show>
  );
}
