export function decidePermission(input: {
  permission?: string;
  type?: string;
  patterns?: string[];
  pattern?: string | string[];
  title?: string;
  metadata?: Record<string, unknown>;
}): "ask" | "allow";
