// web/src/components/settings/storage-format.ts
// Byte formatting shared by the storage settings panels.

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unitIdx = 0;
  while (value >= 1024 && unitIdx < units.length - 1) {
    value /= 1024;
    unitIdx++;
  }
  const decimals = unitIdx >= 2 ? (value >= 100 ? 0 : 1) : 0;
  return `${value.toFixed(decimals)} ${units[unitIdx]}`;
}
