// Mirrors unattendedCommandCategory in internal/runner/approval_unattended.go.
// The shared fixture .opencode/lib/unattended.cases.json keeps both aligned.
// A deny is final: no approval mode and no session grant relaxes it.

const publishRe = /(?:^|[;&|(\n]|&&|\|\|)\s*(?:nohup\s+|time\s+|exec\s+|command\s+|env(?:\s+[A-Za-z_][A-Za-z0-9_]*=\S*)*\s+)*(?:git(?:\s+-C\s+\S+|\s+-c\s+\S+|\s+--git-dir=\S+|\s+--work-tree=\S+)*\s+push|gh\s+(?:release\s+create|pr\s+merge)|(?:npm|pnpm|yarn|bun)\s+(?:-r\s+|--recursive\s+)?publish|cargo\s+publish|twine\s+upload|gem\s+push|docker\s+push|terraform\s+(?:apply|destroy)|kubectl\s+(?:apply|delete)|helm\s+(?:install|upgrade)|(?:\.\/)?scripts\/ai-release\.sh)\b/i;
const sudoRe = /(?:^|[;&|(\n]|&&|\|\|)\s*(?:nohup\s+|time\s+|exec\s+|command\s+|env(?:\s+[A-Za-z_][A-Za-z0-9_]*=\S*)*\s+)*(?:sudo|su|doas)\b/i;
const egressRe = /\b(?:curl|wget|http)\b[^\n]*?(?:authorization|bearer|x-api-key|\$\{?[A-Za-z_][A-Za-z0-9_]*(?:_KEY|_TOKEN|_SECRET)\b)/i;
const secretRe = /(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/(?:\.ssh|\.aws|\.gnupg|\.kube|\.azure)(?:\/|$)|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.docker\/config\.json|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.config\/(?:gh|gcloud)(?:\/|$)|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.(?:netrc|npmrc|pypirc|git-credentials)\b|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/\.cargo\/credentials|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})\/Library\/Keychains(?:\/|$)/i;
const envFileRe = /(?:^|[\s'"=(<>:,])\.env(?:\.(?:local|production|development))?(?:$|[\s;&|'"*?])/;
const wrappedRe = /(?:bash|sh|zsh|eval|xargs)\b[^|&;\n]*\b(?:git\s+push|sudo|publish)\b/i;

export function unattendedCommandDecision(command) {
  const text = String(command ?? "").replace(/<\(([^()]*)\)/g, " $1 ");
  if (publishRe.test(text) || wrappedRe.test(text)) return { expect: "deny", category: "publish" };
  if (sudoRe.test(text)) return { expect: "deny", category: "privilege" };
  if (egressRe.test(text)) return { expect: "deny", category: "egress" };
  if (secretRe.test(text) || envFileRe.test(text)) return { expect: "deny", category: "secret" };
  return { expect: "allow" };
}
