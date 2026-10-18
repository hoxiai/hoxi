// web/src/services/updater-api.ts

export interface UpdateMirrors {
  cn?: string;
  global?: string;
}

export interface UpdateCheckResult {
  currentVersion: string;
  latestVersion: string;
  versionCode: number;
  needUpdate: boolean;
  title: string;
  releaseNotes: string;
  downloadUrl: string;
  mirrors: UpdateMirrors;
  mandatory: boolean;
  sha256?: string;
  checkedAt: string;
}

async function requestUpdateJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, {
    headers: {
      "Content-Type": "application/json",
    },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`HTTP ${res.status}: ${text}`);
  }
  return res.json();
}

export const updaterAPI = {
  checkUpdate(): Promise<UpdateCheckResult> {
    return requestUpdateJSON<UpdateCheckResult>("/api/hoxi/updater/check");
  },
};
