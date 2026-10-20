// web/src/services/recall-api.ts
import { requestJSON } from "./http";

export interface RecallResult {
  sessionID: string;
  title: string;
  runtime?: string;
  role?: string;
  snippet: string;
  score: number;
  updatedAt: number;
  createdAt?: number;
}

export interface RecallSearchResponse {
  directory: string;
  query: string;
  results: RecallResult[];
  sessions: number;
  indexedAt: number;
}

export async function searchSessionRecall(
  directory: string,
  query: string,
  limit = 8
): Promise<RecallSearchResponse> {
  return requestJSON<RecallSearchResponse>(
    `/api/hoxi/recall/search?directory=${encodeURIComponent(directory)}&q=${encodeURIComponent(query)}&limit=${limit}`
  );
}
