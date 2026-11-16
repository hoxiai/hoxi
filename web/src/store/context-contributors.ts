import { createMemo, mapArray, type Accessor } from "solid-js";
import type { MessagePart } from "../services/sse-types";
import type { MessageTurn } from "./workspace-state";

export interface ContextContributor {
  key: string;
  part: MessagePart;
  role: "user" | "assistant";
  tokens: number;
}

function partTokenEstimate(part: MessagePart, inputChars: number): number {
  let chars = 0;
  switch (part.type) {
    case "text": case "reasoning": chars = part.text?.length ?? 0; break;
    case "tool":
      chars = inputChars;
      if (part.state.status === "completed") chars += (part.state.output ?? "").length;
      if (part.state.status === "error") chars += (part.state.error ?? "").length;
      break;
    case "file": chars = (part.filename?.length ?? 0)+(part.url?.length ?? 0); break;
    case "patch": chars = (part.files ?? []).join(",").length; break;
  }
  return Math.ceil(chars/4);
}

/** Each input is serialized only when that input changes, and only while open. */
export function createContextContributors(turns: Accessor<MessageTurn[]>, enabled: Accessor<boolean>): Accessor<ContextContributor[]> {
  const groups = mapArray(() => enabled() ? turns() : [], (turn) => mapArray(() => turn.parts, (part) => {
    const input = createMemo(() => part.type === "tool" ? part.state.input : undefined);
    const inputChars = createMemo(() => {
      if (part.type !== "tool") return 0;
      try { return JSON.stringify(input() ?? {}).length; } catch { return 0; }
    });
    return createMemo<ContextContributor>(() => ({
      key: part.id, part, role: turn.info.role, tokens: partTokenEstimate(part, inputChars()),
    }));
  }));
  return createMemo(() => {
    const top: ContextContributor[] = [];
    // Always read groups, including while closed, so mapArray disposes the old
    // part computations instead of retaining subscriptions to historical inputs.
    for (const group of groups()) {
      for (const candidate of group()) {
        const item = candidate();
        if (item.tokens <= 0) continue;
        const index = top.findIndex((previous) => item.tokens > previous.tokens);
        if (index >= 0) top.splice(index, 0, item);
        else if (top.length < 3) top.push(item);
        if (top.length > 3) top.pop();
      }
    }
    return top;
  }, [], { equals: (a, b) => a.length === b.length && a.every((item, i) => item === b[i]) });
}
