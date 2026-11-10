// Hoxi on-demand sandbox for the OpenCode sandbox plugin. Before a bash
// command runs (either without a session sandbox, or inside a network-allow
// session sandbox that can be tightened), the plugin asks
// Hoxi whether to wrap it. Hoxi answers with a Seatbelt profile only for a
// command it would otherwise ask the user about just because it cannot read
// it; that command then runs confined instead of waiting for approval
// (internal/runner/sandbox_on_demand.go). Imported by sandbox_plugin.mjs; it
// is not a plugin itself and is never registered with OpenCode.

const ROUTE_PATH = "/api/hoxi/runner/sandbox/route";
const ROUTE_TIMEOUT_MS = 3000;
const UNSANDBOXED_MARKER = "# hoxi:unsandboxed";
const SANDBOX_DENIAL_RE = /Operation not permitted|Hoxi sandbox (?:denied|allows only)/;
const SANDBOX_NOTE =
  "\n\n[Hoxi] This command ran in a sandbox because it could not be approved automatically as written. " +
  "In the sandbox it can only write inside the project, temp and cache directories, cannot change .git, " +
  "cannot read .env, key or agent sign-in files, and can only reach servers it starts itself " +
  "(no internet, no local services that were already running, no sockets outside the project). If it failed because of that, " +
  `run it again with \`${UNSANDBOXED_MARKER}\` as its first line to ask the user to approve it outside the sandbox. ` +
  "Do not try to work around the sandbox in any other way.";

// optsOutOfSandbox is true when the model asked to run the command outside
// the sandbox; the command then goes to the user's approval as before.
export function optsOutOfSandbox(command) {
  const first = /^[ \t]*([^\n]*)/.exec(String(command ?? ""))?.[1] ?? "";
  return first.trim() === UNSANDBOXED_MARKER;
}

// onDemandProfile returns the profile path to wrap command in, or undefined
// to run it as it is. Any failure leaves the command unwrapped, so it is
// judged by the normal approval policy.
export async function onDemandProfile({ sessionID, directory, command }) {
  const base = process.env.HOXI_GATEWAY_URL;
  if (!base || typeof directory !== "string" || !directory || typeof command !== "string" || !command.trim()) return undefined;
  if (optsOutOfSandbox(command)) return undefined;
  try {
    const res = await fetch(base.replace(/\/+$/, "") + ROUTE_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sessionID, directory, command }),
      signal: AbortSignal.timeout(ROUTE_TIMEOUT_MS),
      redirect: "error",
    });
    if (!res.ok) return undefined;
    const body = await res.json();
    const profile = body?.profile;
    return typeof profile === "string" && profile.startsWith("/") && profile.endsWith(".sb") ? profile : undefined;
  } catch {
    return undefined;
  }
}

// sandboxNote is appended to the output of an on-demand sandboxed command
// that looks blocked by the sandbox, so the model knows why and how to ask.
export function sandboxNote(output) {
  return typeof output === "string" && SANDBOX_DENIAL_RE.test(output) ? SANDBOX_NOTE : "";
}
