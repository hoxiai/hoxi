export interface NetworkEventTarget {
  addEventListener: (type: string, listener: () => void) => void;
  removeEventListener: (type: string, listener: () => void) => void;
}

export function watchNetworkStatus(
  onChange: (online: boolean) => void,
  target: NetworkEventTarget = window
): () => void {
  const handleOnline = () => onChange(true);
  const handleOffline = () => onChange(false);
  target.addEventListener("online", handleOnline);
  target.addEventListener("offline", handleOffline);

  return () => {
    target.removeEventListener("online", handleOnline);
    target.removeEventListener("offline", handleOffline);
  };
}
