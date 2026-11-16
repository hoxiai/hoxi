// web/src/components/settings/RemoteAccessTabs.tsx
// Switches between the two remote-access transports: the GoPanel reverse tunnel
// and the Cloudflare Tunnel connector. The LAN pairing QR panel stays outside
// this switcher and renders above it.
import { createSignal, Show } from "solid-js";
import { Cloud, Link2 } from "lucide-solid";
import { t } from "../../i18n";
import { SegmentedPills } from "./setting-controls";
import { CloudflaredControls } from "./CloudflaredControls";
import { RemoteTunnelControls } from "./RemoteTunnelControls";

type RemoteAccessTransport = "gopanel" | "cloudflare";

export function RemoteAccessTabs() {
  const [transport, setTransport] = createSignal<RemoteAccessTransport>("gopanel");

  return (
    <div class="space-y-4">
      <SegmentedPills
        value={transport()}
        onChange={(value) => setTransport(value)}
        options={[
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
        ]}
      />
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
