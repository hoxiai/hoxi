package diagnostics

import "regexp"

// Secret redaction shared by diagnostics records and the approval audit log.
// Each rule keeps the surrounding command readable (key names, usernames,
// hosts) and replaces only the secret value with "[redacted]". The rules are
// idempotent so already-redacted text can be passed through again.

const redacted = "[redacted]"

// secretKeys are credential names matched anywhere inside a key, so prefixed
// forms such as PGPASSWORD, GITHUB_TOKEN or x-api-key are covered.
const secretKeys = `api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|client[_-]?secret|` +
	`opencode_server_password|password|passwd|passphrase|sshpass|secret|token|authorization|credentials?`

// shortSecretKeys are too short to match inside other words (bypass, $PWD),
// so they must start the key or follow a separator such as "_", "-" or ".".
// A preceding "$" means a shell variable reference, not an assignment.
const shortSecretKeys = `pass|pwd|pw|auth`

const secretKeyPrefix = `((?:^|[^a-z0-9$])(?:` + shortSecretKeys + `)|` + secretKeys + `)(["']?\s*[=:]\s*)`

var (
	// Values that are secret on their own: bearer tokens, vendor-prefixed keys
	// and PEM private key blocks (possibly cut off by truncation). "Basic" is
	// only redacted after an Authorization key: on its own it is a common word.
	secretTokenRe = regexp.MustCompile(`(?i)\bbearer\s+[^\s"']+|` +
		`\b(?:sk-ant-|sk-|ghp_|gho_|ghu_|ghs_|ghr_|github_pat_|glpat-|xox[abprs]-)[a-z0-9_\-]{8,}|\bAKIA[0-9A-Z]{16}\b`)
	privateKeyBlockRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)
	// scheme://user:password@host keeps the user and host.
	urlUserinfoRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.\-]*://[^\s/?#@:'"]*):([^\s/?#@'"]+)@`)
	// curl -u user:pass / --user user:pass keeps the user.
	curlUserRe = regexp.MustCompile(`(\bcurl\b[^|;&\n]*?\s(?:-u\s*|--user(?:\s+|=))["']?[^\s:"']*):([^\s"']+)`)
	// mysql -p<pwd> takes the password attached to the flag; "-p" alone
	// prompts and "-P" is the port, so this rule is case sensitive.
	mysqlPasswordRe = regexp.MustCompile(`(\b(?:mysql|mysqldump|mysqladmin|mysqlimport|mysqlcheck|mysqlshow|mysqlsh|mariadb|mariadb-dump)\b[^|;&\n]*?\s-p)(?:"[^"]*"|'[^']*'|[^\s"']+)`)
	// sshpass -p and docker/podman login -p take the password attached or as
	// the next argument.
	loginPasswordRe = regexp.MustCompile(`(\b(?:sshpass|docker\s+login|podman\s+login)\b[^|;&\n]*?\s-p\s*)(?:"[^"]*"|'[^']*'|[^\s"'-][^\s"']*)`)
	// --password value, --token value (space separated long flags).
	flagSecretRe = regexp.MustCompile(`(?i)(--(?:password|passwd|pass|passphrase|token|auth-token|access-token|api-key|secret|client-secret)\s+)(?:"[^"]*"|'[^']*'|[^\s"'-][^\s"']*)`)
	// key="value", key='value', "key":"value" keep the quotes so the preview
	// does not look like a truncated command.
	secretQuotedRe = regexp.MustCompile(`(?i)` + secretKeyPrefix + `(?:(")[^"]*(")|(')[^']*('))`)
	// key=value, key: value, Authorization: Basic xxx.
	secretBareRe = regexp.MustCompile(`(?i)` + secretKeyPrefix + `(?:(?:basic|bearer|digest|token)\s+)?[^\s,;"'}&|)<>]+`)
)

// RedactSecrets replaces credential values in v with "[redacted]". It does
// not bound or otherwise normalize v; callers truncate before and after.
func RedactSecrets(v string) string {
	v = privateKeyBlockRe.ReplaceAllString(v, redacted)
	v = secretTokenRe.ReplaceAllString(v, redacted)
	v = urlUserinfoRe.ReplaceAllString(v, "${1}:"+redacted+"@")
	v = curlUserRe.ReplaceAllString(v, "${1}:"+redacted)
	v = mysqlPasswordRe.ReplaceAllString(v, "${1}"+redacted)
	v = loginPasswordRe.ReplaceAllString(v, "${1}"+redacted)
	v = flagSecretRe.ReplaceAllString(v, "${1}"+redacted)
	v = secretQuotedRe.ReplaceAllString(v, "${1}${2}${3}${5}"+redacted+"${4}${6}")
	v = secretBareRe.ReplaceAllString(v, "${1}${2}"+redacted)
	return v
}
