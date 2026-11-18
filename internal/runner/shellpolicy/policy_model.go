package shellpolicy

import (
	"fmt"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/dataprofiles"
	"github.com/hoxiai/hoxi/internal/runner/sessionstore"
)

type SessionRecord = sessionstore.SessionRecord

const (
	RuntimeOpenCode    = sessionstore.RuntimeOpenCode
	RuntimeCodex       = sessionstore.RuntimeCodex
	RuntimeAntigravity = sessionstore.RuntimeAntigravity
	RuntimeClaudeCode  = sessionstore.RuntimeClaudeCode
	RuntimeGeminiCLI   = sessionstore.RuntimeGeminiCLI
	RuntimeCodeBuddy   = sessionstore.RuntimeCodeBuddy
	RuntimeQoder       = sessionstore.RuntimeQoder
	RuntimeNative      = sessionstore.RuntimeNative
)

const (
	StopMarkerPrefix     = "# hoxi-stop:"
	ToolStoppedNotice    = "[Command stopped by user]"
	OpenCodeStopTrap     = `trap 'printf "\n` + ToolStoppedNotice + `\n"; exit 130' USR1; trap : EXIT`
	OpenCodeStopTrapUSR1 = `trap 'printf "\n` + ToolStoppedNotice + `\n"; exit 130' USR1`

	stopMarkerPrefix     = StopMarkerPrefix
	openCodeStopTrap     = OpenCodeStopTrap
	openCodeStopTrapUSR1 = OpenCodeStopTrapUSR1
)

// ApprovalPolicy is the user's approval mode plus per-tool overrides. It lives
// in ~/.hoxi/approval-policy.json so the decision is the same whether a
// request is answered by the desktop, the CLI, the phone, or nobody is
// watching. An empty override follows the mode's preset.
type ApprovalPolicy struct {
	Mode     string `json:"mode"`
	Edit     string `json:"edit,omitempty"`     // "" | "allow" | "ask"
	Webfetch string `json:"webfetch,omitempty"` // "" | "allow" | "ask"
	Bash     string `json:"bash,omitempty"`     // "" | "allow" | "smart" | "ask"
	// ApprovalTimeoutSeconds is how long a pending request waits before it is
	// rejected. Zero means the default. A timeout only ever rejects.
	ApprovalTimeoutSeconds int   `json:"approvalTimeoutSeconds,omitempty"`
	UpdatedAt              int64 `json:"updatedAt,omitempty"`
}

const (
	DefaultApprovalTimeoutSeconds = 600
	MinApprovalTimeoutSeconds     = 60
	MaxApprovalTimeoutSeconds     = 3600
)

func DefaultApprovalPolicy() ApprovalPolicy {
	return ApprovalPolicy{Mode: ApprovalModeAuto}
}

// ApprovalWaitLimit is how long a pending request waits. Zero means default.
func (p ApprovalPolicy) ApprovalWaitLimit() time.Duration {
	seconds := p.ApprovalTimeoutSeconds
	if seconds <= 0 {
		seconds = DefaultApprovalTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func (p ApprovalPolicy) approvalWaitLimit() time.Duration {
	return p.ApprovalWaitLimit()
}

func ApprovalTimeoutReason(p ApprovalPolicy) string {
	minutes := int(p.ApprovalWaitLimit() / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("Approval timed out after %d minute(s) with no response and was rejected. Try an approach that does not need this permission, or say so in the summary.", minutes)
}

func UnattendedQuestionTimeoutReason() string {
	return "The user is away. Choose the safest plan that needs no approval, and say what you assumed."
}

func IsQuestionRequest(req map[string]interface{}) bool {
	name := strings.ToLower(strings.TrimSpace(permissionName(req)))
	return name == "question" || name == "ask_question" || strings.HasSuffix(name, ".question")
}

// EffectiveRule resolves a category's rule from its override or the mode preset.
func (p ApprovalPolicy) EffectiveRule(category string) string {
	return p.effectiveRule(category)
}

func (p ApprovalPolicy) effectiveRule(category string) string {
	switch category {
	case permissionCategoryEdit:
		if p.Edit != "" {
			return p.Edit
		}
	case permissionCategoryWebfetch:
		if p.Webfetch != "" {
			return p.Webfetch
		}
	case permissionCategoryBash:
		if p.Bash != "" {
			return p.Bash
		}
	}
	switch p.Mode {
	case ApprovalModeManual:
		return "ask"
	case ApprovalModeSmart:
		if category == permissionCategoryBash {
			return "smart"
		}
	}
	return "allow"
}

// Validate rejects unknown values instead of silently dropping them.
func (p ApprovalPolicy) Validate() error { return p.validate() }

func (p ApprovalPolicy) validate() error {
	switch p.Mode {
	case ApprovalModeAuto, ApprovalModeSmart, ApprovalModeManual, ApprovalModeTrusted, ApprovalModeUnattended:
	default:
		return fmt.Errorf("mode must be auto, smart, manual, trusted or unattended")
	}
	if p.ApprovalTimeoutSeconds != 0 && (p.ApprovalTimeoutSeconds < MinApprovalTimeoutSeconds || p.ApprovalTimeoutSeconds > MaxApprovalTimeoutSeconds) {
		return fmt.Errorf("approvalTimeoutSeconds must be between %d and %d", MinApprovalTimeoutSeconds, MaxApprovalTimeoutSeconds)
	}
	for name, value := range map[string]string{"edit": p.Edit, "webfetch": p.Webfetch} {
		if value != "" && value != "allow" && value != "ask" {
			return fmt.Errorf("%s must be empty, allow or ask", name)
		}
	}
	if p.Bash != "" && p.Bash != "allow" && p.Bash != "smart" && p.Bash != "ask" {
		return fmt.Errorf("bash must be empty, allow, smart or ask")
	}
	return nil
}

func firstString(vals ...interface{}) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func isReadOnlyDataSQLQuery(query string) (bool, string) {
	return dataprofiles.IsReadOnlySQL(query)
}

func isReadOnlyDataSSHCommand(command string) (bool, string) {
	return dataprofiles.IsReadOnlySSHCommand(command)
}

var (
	credentialEnvNames = setOf(
		"SSH_AUTH_SOCK", "SSH_AGENT_PID", "GPG_AGENT_INFO",
		"GIT_ASKPASS", "SSH_ASKPASS", "SUDO_ASKPASS",
		"KUBECONFIG", "DOCKER_CONFIG", "NETRC", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE",
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	)
	credentialNameFragments = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "APIKEY", "API_KEY", "ACCESS_KEY", "PRIVATE_KEY"}
	credentialNameParts     = setOf("AUTH", "PAT", "AUTHTOKEN")
)

func IsCredentialEnvName(name string) bool { return isCredentialEnvName(name) }

func isCredentialEnvName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return false
	}
	if credentialEnvNames[upper] || strings.HasPrefix(upper, "GIT_CONFIG_KEY_") || strings.HasPrefix(upper, "GIT_CONFIG_VALUE_") {
		return true
	}
	for _, fragment := range credentialNameFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	for _, part := range strings.Split(upper, "_") {
		if credentialNameParts[part] {
			return true
		}
	}
	return strings.HasSuffix(upper, "_KEY")
}
