// Cross-platform lexical path helpers for the approval policy JS mirror.
// Pure leaf module (zero imports). Mirrors shell_paths.go and
// normalizeWindowsDrivePaths in permission_policy.go.

const WINDOWS_DRIVE_ABS_RE = /^[A-Za-z]:[\\/]/;
const WINDOWS_DRIVE_ROOT_RE = /^[A-Za-z]:\/*$/;
const WINDOWS_TEMP_PATH_RE = /^[a-z]:\/(?:users\/[^/]+\/appdata\/local\/temp|windows\/temp)\/[^/]+(?:\/.*)?$/i;
const WINDOWS_QUOTED_DRIVE_PATH_RE = /("([a-z]:\\[^"\r\n$`]*[^"\r\n$`\\])"|'([a-z]:\\[^'\r\n]*[^'\r\n\\])')/gi;
const WINDOWS_UNQUOTED_DRIVE_PATH_RE = /(^|[\s=;&|()])([a-z]:\\[^\s;&|'"`()<>]*)/gi;

export function isWindowsDriveAbs(raw) {
  return WINDOWS_DRIVE_ABS_RE.test(String(raw || ""));
}

export function isWindowsDriveRoot(raw) {
  return WINDOWS_DRIVE_ROOT_RE.test(String(raw || "").trim().replaceAll("\\", "/"));
}

export function isAbsPolicyPath(raw) {
  const s = String(raw || "");
  return s.startsWith("/") || isWindowsDriveAbs(s);
}

export function cleanPolicyPath(raw) {
  const s = String(raw || "").trim().replaceAll("\\", "/");
  if (!s) return "";
  const winDrive = isWindowsDriveAbs(s) ? s[0].toUpperCase() + ":" : "";
  const abs = Boolean(winDrive) || s.startsWith("/");
  const body = winDrive ? s.slice(2) : s;
  const parts = [];
  for (const seg of body.split("/")) {
    if (!seg || seg === ".") continue;
    if (seg === "..") {
      if (parts.length > 0 && parts[parts.length - 1] !== "..") parts.pop();
      else if (!abs) parts.push("..");
    } else {
      parts.push(seg);
    }
  }
  const joined = parts.join("/");
  if (winDrive) return `${winDrive}/${joined}`;
  return abs ? "/" + joined : joined || ".";
}

export function joinPolicyPath(a, b) {
  if (!a) return b;
  if (!b) return a;
  const bs = b.replaceAll("\\", "/");
  if (isAbsPolicyPath(bs)) return bs;
  return a.replaceAll("\\", "/").replace(/\/+$/, "") + "/" + bs;
}

export function isWindowsTempPath(raw) {
  const cleaned = cleanPolicyPath(raw);
  if (!WINDOWS_TEMP_PATH_RE.test(cleaned)) return false;
  return cleaned.slice(3).split("/").every((part) => part !== "" && part !== "." && part !== ".." && !/[*?[]/.test(part));
}

export function policyPathWithin(base, target) {
  const b = cleanPolicyPath(base);
  const t = cleanPolicyPath(target);
  if (!b || !t || b === "/" || isWindowsDriveRoot(b)) return false;
  const bWin = isWindowsDriveAbs(b);
  const tWin = isWindowsDriveAbs(t);
  if (bWin !== tWin) return false;
  if (bWin) {
    const bl = b.toLowerCase().replace(/\/+$/, "");
    const tl = t.toLowerCase().replace(/\/+$/, "");
    return tl === bl || tl.startsWith(bl + "/");
  }
  const bp = b.replace(/\/+$/, "");
  return t === bp || t.startsWith(bp + "/");
}

function looksLikeWindowsDrivePath(p) {
  if (!p || p.length < 4 || p[1] !== ":" || p[2] !== "\\") return false;
  const rest = p.slice(3);
  return rest.includes("\\") || rest.length >= 2;
}

export function normalizeWindowsDrivePaths(command) {
  const s = String(command ?? "");
  if (!s.includes(":\\")) return s;
  const quoted = s.replace(WINDOWS_QUOTED_DRIVE_PATH_RE, (m) => {
    const inner = m.slice(1, -1);
    if (!looksLikeWindowsDrivePath(inner)) return m;
    return m[0] + inner.replaceAll("\\", "/") + m[m.length - 1];
  });
  return quoted.replace(WINDOWS_UNQUOTED_DRIVE_PATH_RE, (m, prefix, token) => {
    if (!looksLikeWindowsDrivePath(token)) return m;
    return prefix + token.replaceAll("\\", "/");
  });
}
