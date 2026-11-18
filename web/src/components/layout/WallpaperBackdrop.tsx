// web/src/components/layout/WallpaperBackdrop.tsx
import { createMemo, Show } from "solid-js";
import {
  composeLayeredWallpaperStyle,
  resolveActiveWallpaperStandee,
  resolveActiveWallpaperUrl,
  wallpaperState,
} from "../../store/workspace";

export default function WallpaperBackdrop() {
  const activeUrl = createMemo(() => resolveActiveWallpaperUrl());
  const activeStandee = createMemo(() => resolveActiveWallpaperStandee());
  const layerStyle = createMemo(() => {
    const standee = activeStandee();
    return {
      ...composeLayeredWallpaperStyle(
        activeUrl(),
        standee?.url,
        standee?.positionX,
        "72% center",
        standee ? { standeeSize: standee.viewportSize } : undefined
      ),
      filter:
        "contrast(var(--wallpaper-img-contrast, 0.88)) brightness(var(--wallpaper-img-brightness, 1)) saturate(1.04)",
    };
  });

  return (
    <Show when={wallpaperState.enabled && Boolean(activeUrl())}>
      <div
        aria-hidden="true"
        class="pointer-events-none fixed inset-0 z-0 overflow-hidden select-none"
      >
        <div
          class="absolute inset-0 transition-[background-image,transform,filter] duration-300"
          style={layerStyle()}
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
