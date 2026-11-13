import { matchSearchRanges, matchSearchTurns, type SearchRequest, type SearchResponse } from "./transcript-search-engine";

const scope = globalThis as unknown as {
  onmessage: (event: MessageEvent<SearchRequest>) => void;
  postMessage(response: SearchResponse): void;
};
scope.onmessage = (event) => {
  const request = event.data;
  scope.postMessage("turns" in request
    ? { id: request.id, matches: matchSearchTurns(request.turns, request.pattern) }
    : { id: request.id, ranges: matchSearchRanges(request.texts, request.pattern, Math.min(300, request.limit)) });
};
