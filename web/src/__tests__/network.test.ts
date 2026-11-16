import { assertDeepEqual, describe, it } from "./test-harness";
import { watchNetworkStatus, type NetworkEventTarget } from "../services/network";

describe("network status watcher", () => {
  it("reports online and offline transitions and removes listeners", () => {
    const listeners = new Map<string, Set<() => void>>();
    const target: NetworkEventTarget = {
      addEventListener: (type, listener) => {
        const callbacks = listeners.get(type) ?? new Set();
        callbacks.add(listener as () => void);
        listeners.set(type, callbacks);
      },
      removeEventListener: (type, listener) => {
        listeners.get(type)?.delete(listener as () => void);
      },
    };
    const changes: boolean[] = [];
    const stop = watchNetworkStatus((online) => changes.push(online), target);

    listeners.get("offline")?.forEach((listener) => listener());
    listeners.get("online")?.forEach((listener) => listener());
    stop();
    listeners.get("offline")?.forEach((listener) => listener());

    assertDeepEqual(changes, [false, true]);
    assertDeepEqual([...listeners.values()].map((callbacks) => callbacks.size), [0, 0]);
  });
});
