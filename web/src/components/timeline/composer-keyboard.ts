export function isComposerIMEEvent(
  event: Pick<KeyboardEvent, "isComposing" | "key" | "keyCode">,
  composing: boolean,
): boolean {
  // WebKit can report isComposing=false for the Enter that commits IME text.
  return composing || event.isComposing || event.keyCode === 229 || event.key === "Process";
}
