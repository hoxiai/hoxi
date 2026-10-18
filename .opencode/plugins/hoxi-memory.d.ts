export interface SessionLedger {
  goals: string[];
  constraints: string[];
  facts: string[];
  changed: Map<string, string>;
  errors: string[];
  todos: string[];
  progress: string;
  latestUser: string;
}

export function createSessionLedger(): SessionLedger;
export function ingestMessages(
  ledger: SessionLedger,
  messages: Array<{
    info?: { id?: string; role?: string };
    parts?: Array<Record<string, unknown>>;
  }>
): void;
export function ledgerHasEvidence(ledger: SessionLedger): boolean;
export function noteDiffs(
  ledger: SessionLedger,
  diffs: Array<{ file?: string; additions?: number; deletions?: number }>
): void;
export function noteTodos(
  ledger: SessionLedger,
  todos: Array<{ content?: string; status?: string }>
): void;
export function renderEightDimMemory(
  sessionID: string,
  ledger: SessionLedger,
  updatedAt?: string
): string;
