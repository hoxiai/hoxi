// web/src/services/auth-api.ts

export interface AuthUserProfile {
  id: number;
  email: string;
  nickname: string;
  username?: string;
  avatar?: string;
  avatarUrl?: string;
  role?: string;
  plan?: string;
}

export interface AuthStatusSnapshot {
  isLoggedIn: boolean;
  isAuthorizing: boolean;
  pendingState?: string;
  user?: AuthUserProfile;
  expiresAt?: string;
  serverBaseUrl: string;
  lastVerifiedAt?: string;
}

export interface StartAuthResponse {
  state: string;
  authUrl: string;
}

async function requestAuthJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers || {}),
    },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`HTTP ${res.status}: ${text}`);
  }
  return res.json();
}

export const authAPI = {
  getStatus(): Promise<AuthStatusSnapshot> {
    return requestAuthJSON<AuthStatusSnapshot>("/api/hoxi/auth/status");
  },

  startAuth(): Promise<StartAuthResponse> {
    return requestAuthJSON<StartAuthResponse>("/api/hoxi/auth/start", {
      method: "POST",
    });
  },

  exchange(handoffToken: string, state: string): Promise<{ success: boolean; user: AuthUserProfile }> {
    return requestAuthJSON("/api/hoxi/auth/exchange", {
      method: "POST",
      body: JSON.stringify({ handoffToken, state }),
    });
  },

  logout(): Promise<{ success: boolean }> {
    return requestAuthJSON("/api/hoxi/auth/logout", {
      method: "POST",
    });
  },
};
