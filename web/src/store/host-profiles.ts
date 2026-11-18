// web/src/store/host-profiles.ts
import { createSignal } from "solid-js";
import {
  hostProfilesAPI,
  type DataProfileItem,
  type DataProfileUpsertPayload,
  type RemoteHostItem,
  type RemoteHostProbeResponse,
  type RemoteHostUpsertPayload,
} from "../services/host-profiles-api";

const [dataProfiles, setDataProfiles] = createSignal<DataProfileItem[]>([]);
const [remoteHosts, setRemoteHosts] = createSignal<RemoteHostItem[]>([]);

export { dataProfiles, remoteHosts };

export async function loadDataProfiles(): Promise<DataProfileItem[]> {
  const list = await hostProfilesAPI.listDataProfiles();
  setDataProfiles(list);
  return list;
}

export async function upsertDataProfile(payload: DataProfileUpsertPayload): Promise<DataProfileItem> {
  const saved = await hostProfilesAPI.saveDataProfile(payload);
  await loadDataProfiles();
  return saved;
}

export async function removeDataProfile(id: string): Promise<void> {
  await hostProfilesAPI.deleteDataProfile(id);
  setDataProfiles((prev) => prev.filter((item) => item.id !== id));
}

export async function runDataProfileTest(id: string): Promise<Record<string, any>> {
  return hostProfilesAPI.testDataProfile(id);
}

export async function loadRemoteHosts(): Promise<RemoteHostItem[]> {
  const list = await hostProfilesAPI.listRemoteHosts();
  setRemoteHosts(list);
  return list;
}

export async function upsertRemoteHost(payload: RemoteHostUpsertPayload): Promise<RemoteHostItem> {
  const saved = await hostProfilesAPI.saveRemoteHost(payload);
  await loadRemoteHosts();
  return saved;
}

export async function removeRemoteHost(id: string): Promise<void> {
  await hostProfilesAPI.deleteRemoteHost(id);
  setRemoteHosts((prev) => prev.filter((item) => item.id !== id));
}

export async function probeRemoteHostById(id: string): Promise<RemoteHostProbeResponse> {
  const res = await hostProfilesAPI.probeRemoteHost(id);
  if (res?.host?.id) {
    setRemoteHosts((prev) =>
      prev.map((item) => (item.id === res.host.id ? res.host : item))
    );
  }
  return res;
}
