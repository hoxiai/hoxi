// web/src/components/settings/RemoteAccessTabs.tsx
// Switches between remote-access transports: optional extension relay tabs,
// the GoPanel reverse tunnel, and the Cloudflare Tunnel connector. The LAN
// pairing QR panel stays outside this switcher and renders above it.
import { createSignal, For, Show } from "solid-js";
import { Cloud, Globe, Link2 } from "lucide-solid";
import { t } from "../../i18n";
import { surfaceExtension } from "../layout/surface-extension";
import { SegmentedPills } from "./setting-controls";
import { CloudflaredControls } from "./CloudflaredControls";
import { RemoteTunnelControls } from "./RemoteTunnelControls";

export function RemoteAccessTabs() {
  const extTabs = () =>
    surfaceExtension.enabled && surfaceExtension.remoteAccessTabs
      ? surfaceExtension.remoteAccessTabs()
      : [];
  const [transport, setTransport] = createSignal<string>(extTabs()[0]?.id ?? "gopanel");

  const options = () => [
    ...extTabs().map((tab) => ({
      id: tab.id,
      label: tab.label,
      tip: tab.tip,
      icon: Globe,
    })),
    {
      id: "gopanel",
      label: () => t("remoteTunnel.tabLabel"),
      tip: () => t("remoteTunnel.title"),
      icon: Link2,
    },
    {
      id: "cloudflare",
      label: () => t("cloudflared.tabLabel"),
      tip: () => t("cloudflared.title"),
      icon: Cloud,
    },
  ];

  return (
    <div class="space-y-4">
      <SegmentedPills
        value={transport()}
        onChange={(value) => setTransport(value)}
        options={options()}
      />
      <For each={extTabs()}>
        {(tab) => (
          <Show when={transport() === tab.id}>
            <div class="hoxi-view-enter">{tab.render()}</div>
          </Show>
        )}
      </For>
      <Show when={transport() === "gopanel"}>
        <div class="hoxi-view-enter">
          <RemoteTunnelControls />
        </div>
      </Show>
      <Show when={transport() === "cloudflare"}>
        <div class="hoxi-view-enter">
          <CloudflaredControls />
        </div>
      </Show>
    </div>
  );
}
