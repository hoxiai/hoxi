package shellpolicy

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Session-scoped single-host SSH grants let the desktop user approve one remote
// host (or one named hoxi_data SSH profile) once per session instead of being
// re-prompted on every subsequent command to that same host. Grants live in
// session memory only, are revocable in Settings, are never granted over a
// remote tunnel, and never relax local hard floors (localhost/loopback targets,
// ProxyCommand/LocalCommand/IdentityFile options, local input redirects like
// `< .env`, writes outside /tmp, or local destructive/opaque commands).

const (
	sshHostGrantPrefix    = "ssh:"
	sshProfileGrantPrefix = "ssh-profile:"
	grantKindSSH          = "ssh"
	maxSSHGrantHosts      = 2
)

var (
	sshProfileNameRe         = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,128}$`)
	sshOutRedirOnlyRe        = regexp.MustCompile(`^(?:[12]|&)?>>?$`)
	sshOutRedirAttachRe      = regexp.MustCompile(`^(?:[12]|&)?>>?(.+)$`)
	sshGrantExtraUnsafeOptRe = regexp.MustCompile(`(?i)(?:logfile|localforward|remoteforward|dynamicforward|controlpath|remotecommand|proxyjump)`)
)

func isSSHGrantClass(class string) bool {
	_, _, ok := sshGrantTarget(class)
	return ok
}

func sshGrantTarget(class string) (kind, target string, ok bool) {
	switch {
	case strings.HasPrefix(class, sshHostGrantPrefix):
		host := strings.TrimSpace(strings.TrimPrefix(class, sshHostGrantPrefix))
		if host != "" && !strings.ContainsAny(host, "@ \t\r\n") && isNonLoopbackSSHHost(host) {
			return "host", host, true
		}
	case strings.HasPrefix(class, sshProfileGrantPrefix):
		profile := strings.TrimSpace(strings.TrimPrefix(class, sshProfileGrantPrefix))
		if isValidSSHProfileName(profile) {
			return "profile", profile, true
		}
	}
	return "", "", false
}

func isValidSSHProfileName(profile string) bool {
	if profile == "" || profile == "." || profile == ".." || strings.HasPrefix(profile, "-") || strings.Contains(profile, "..") {
		return false
	}
	return sshProfileNameRe.MatchString(profile)
}

// grantableSSHRequestClasses returns the sorted single-host SSH session grant
// class(es) for a hoxi_data ssh_exec call or a bash remote ssh/scp command, or
// nil when any segment falls outside the strict SSH grant boundary.
func grantableSSHRequestClasses(item map[string]interface{}, worktree string) []string {
	if classes := grantableSSHProfileClasses(item); len(classes) > 0 {
		return classes
	}
	if strings.TrimSpace(worktree) == "" {
		return nil
	}
	return grantableBashSSHClasses(item, worktree)
}

func grantableSSHProfileClasses(item map[string]interface{}) []string {
	meta := itemMetadata(item)
	if external, _ := meta["externalDirectory"].(bool); external {
		return nil
	}
	perm := permissionName(item)
	toolObj, _ := item["tool"].(map[string]interface{})
	toolName := strings.ToLower(strings.TrimSpace(firstString(meta["tool"], toolObj["name"], firstPattern(item))))
	if perm != "hoxi_data" && toolName != "hoxi_data" {
		return nil
	}
	args := parseToolArgs(meta["args"])
	action := strings.ToLower(strings.TrimSpace(firstString(meta["action"], args["action"])))
	if action != "ssh_exec" {
		return nil
	}
	profile := strings.TrimSpace(firstString(meta["profile"], args["profile"]))
	if !isValidSSHProfileName(profile) {
		return nil
	}
	return []string{sshProfileGrantPrefix + profile}
}

func grantableBashSSHClasses(item map[string]interface{}, worktree string) []string {
	if !isControllableShellItem(item) {
		return nil
	}
	candidates := opacityCandidates(item)
	if len(candidates) == 0 {
		return nil
	}
	hosts := map[string]bool{}
	for _, cmd := range candidates {
		segHosts, ok := bashSSHCommandHosts(cmd, worktree)
		if !ok || len(segHosts) == 0 {
			return nil
		}
		for _, h := range segHosts {
			hosts[h] = true
		}
	}
	if len(hosts) == 0 || len(hosts) > maxSSHGrantHosts {
		return nil
	}
	meta := itemMetadata(item)
	if whole := strings.TrimSpace(unwrapSandboxCommand(firstString(meta["command"]))); whole != "" {
		for _, piece := range commandCandidates(item) {
			piece = strings.TrimSpace(piece)
			if piece == "" || strings.Contains(whole, piece) {
				continue
			}
			if isDestructiveCommand(piece) || isOpaqueShellCommandWith(piece, worktree, scriptTrustNone) {
				return nil
			}
		}
	}
	out := make([]string, 0, len(hosts))
	for h := range hosts {
		out = append(out, sshHostGrantPrefix+h)
	}
	sort.Strings(out)
	return out
}

func stripUnsandboxedHeader(command string) string {
	rest := strings.TrimLeft(command, "\r\n")
	if !optsOutOfOnDemandSandbox(rest) {
		return strings.TrimSpace(command)
	}
	_, after, _ := strings.Cut(rest, "\n")
	return strings.TrimSpace(after)
}

func bashSSHCommandHosts(rawCmd, worktree string) ([]string, bool) {
	cmd := stripUnsandboxedHeader(unwrapSandboxCommand(rawCmd))
	if cmd == "" {
		return nil, false
	}
	cmd = joinLineContinuations(normalizeWindowsDrivePaths(cmd))
	hadHeredoc := false
	if strings.Contains(cmd, "<<") {
		stripped, ok := stripLiteralHeredocs(cmd)
		if !ok {
			return nil, false
		}
		cmd = stripped
		hadHeredoc = true
	}
	if hasShellGrouping(cmd) {
		return nil, false
	}
	segments, pipedIn := splitCommandChainWithPipes(cmd)
	if len(segments) == 0 {
		return nil, false
	}
	cwd := strings.TrimSpace(worktree)
	if cwd == "" {
		cwd = "."
	}
	hosts := map[string]bool{}
	for i, rawSeg := range segments {
		seg := strings.TrimSpace(rawSeg)
		if seg == "" || strings.HasPrefix(seg, "#") {
			continue
		}
		if host, ok := safeRemoteSSHGrantHost(seg, hadHeredoc && !pipedIn[i]); ok {
			hosts[host] = true
			continue
		}
		if scpHosts, ok := safeRemoteSCPGrantHosts(seg); ok {
			for _, h := range scpHosts {
				hosts[h] = true
			}
			continue
		}
		if nextCWD, ok := safeSSHCompanionSegment(seg, cwd, worktree, i > 0 && pipedIn[i]); ok {
			cwd = nextCWD
			continue
		}
		return nil, false
	}
	if len(hosts) == 0 || len(hosts) > maxSSHGrantHosts {
		return nil, false
	}
	out := make([]string, 0, len(hosts))
	for h := range hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, true
}

func normalizeSSHHost(target string) (string, bool) {
	if !isNonLoopbackSSHHost(target) {
		return "", false
	}
	m := sshHostSpecRe.FindStringSubmatch(target)
	if m == nil {
		return "", false
	}
	host := strings.TrimRight(strings.ToLower(m[1]), ".")
	if host == "" {
		return "", false
	}
	return host, true
}

func isSafeSSHTempLogTarget(raw string) bool {
	target := strings.TrimSpace(raw)
	if target == "" || strings.ContainsAny(target, "*?[$`~<>;&|()'\"") {
		return false
	}
	return isTempPathArg(target)
}

func unwrapSSHGrantWords(seg string) ([]string, bool) {
	tokens, ok := scanSSHSegmentWords(seg)
	if !ok {
		return nil, false
	}
	for len(tokens) > 0 && !tokens[0].quoted && shellKeywords[tokens[0].val] {
		tokens = tokens[1:]
	}
	words := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if !tok.quoted {
			if devNullWordRe.MatchString(tok.val) || dupStreamWordRe.MatchString(tok.val) {
				continue
			}
			if redirectWordRe.MatchString(tok.val) && i+1 < len(tokens) && !tokens[i+1].quoted && tokens[i+1].val == "/dev/null" {
				i++
				continue
			}
			if sshOutRedirOnlyRe.MatchString(tok.val) && i+1 < len(tokens) && !tokens[i+1].unquotedRedir && isSafeSSHTempLogTarget(tokens[i+1].val) {
				i++
				continue
			}
			if m := sshOutRedirAttachRe.FindStringSubmatch(tok.val); m != nil && !strings.Contains(tok.val, "<") && isSafeSSHTempLogTarget(m[1]) {
				continue
			}
		}
		if tok.unquotedRedir {
			return nil, false
		}
		words = append(words, tok.val)
	}
	if len(words) == 0 {
		return nil, false
	}
	j := 0
	for j < len(words) && envVarAssignmentWordRe.MatchString(words[j]) {
		j++
	}
	if j >= len(words) {
		return nil, false
	}
	unwrapped, lookupOnly, ok := unwrapCommandPrefixes(words[j:])
	if !ok || lookupOnly || len(unwrapped) == 0 {
		return nil, false
	}
	for _, w := range words[:len(words)-len(unwrapped)] {
		if strings.ContainsAny(w, "$<>") {
			return nil, false
		}
	}
	if filepath.Base(normalizeAbsoluteTool(unwrapped[0])) == "timeout" {
		if len(unwrapped) < 3 || !sshDurationRe.MatchString(unwrapped[1]) {
			return nil, false
		}
		unwrapped = unwrapped[2:]
	}
	return unwrapped, true
}

func safeRemoteSSHGrantHost(seg string, allowNoRemoteArg bool) (string, bool) {
	words, ok := unwrapSSHGrantWords(seg)
	if !ok || filepath.Base(normalizeAbsoluteTool(words[0])) != "ssh" {
		return "", false
	}
	args := words[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.ContainsAny(a, "<>") {
			return "", false
		}
		switch {
		case a == "-i":
			if i+1 >= len(args) || !isSSHIdentityKeyArg(args[i+1]) {
				return "", false
			}
			i++
		case strings.HasPrefix(a, "-i") && len(a) > 2:
			if !isSSHIdentityKeyArg(a[2:]) {
				return "", false
			}
		case a == "-o":
			if i+1 >= len(args) || strings.ContainsAny(args[i+1], "<>$") || sshUnsafeOptRe.MatchString(args[i+1]) || sshGrantExtraUnsafeOptRe.MatchString(args[i+1]) {
				return "", false
			}
			i++
		case strings.HasPrefix(a, "-o") && len(a) > 2:
			if strings.Contains(a, "$") || sshUnsafeOptRe.MatchString(a[2:]) || sshGrantExtraUnsafeOptRe.MatchString(a[2:]) {
				return "", false
			}
		case a == "-p" || a == "-l":
			if i+1 >= len(args) || strings.ContainsAny(args[i+1], "<>$") {
				return "", false
			}
			i++
		case strings.HasPrefix(a, "-p") && len(a) > 2:
			if strings.Contains(a, "$") {
				return "", false
			}
		case sshSafeShortFlagRe.MatchString(a):
		case strings.HasPrefix(a, "-"):
			return "", false
		default:
			if !allowNoRemoteArg && i+1 >= len(args) {
				return "", false
			}
			return normalizeSSHHost(a)
		}
	}
	return "", false
}

func safeRemoteSCPGrantHosts(seg string) ([]string, bool) {
	if _, ok := rewriteSafeRemoteSCPSegment(seg); !ok {
		return nil, false
	}
	words, ok := unwrapSSHCommandWords(seg)
	if !ok || filepath.Base(normalizeAbsoluteTool(words[0])) != "scp" {
		return nil, false
	}
	hosts := map[string]bool{}
	args := words[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-o" {
			if i+1 >= len(args) || sshGrantExtraUnsafeOptRe.MatchString(args[i+1]) {
				return nil, false
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "-o") && len(a) > 2 {
			if sshGrantExtraUnsafeOptRe.MatchString(a[2:]) {
				return nil, false
			}
			continue
		}
		if a == "-i" || a == "-P" || a == "-l" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if colon := strings.IndexByte(a, ':'); colon > 0 && !strings.HasPrefix(a, "/") && !strings.HasPrefix(a, ".") {
			host, ok := normalizeSSHHost(a[:colon])
			if !ok {
				return nil, false
			}
			hosts[host] = true
		}
	}
	if len(hosts) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(hosts))
	for h := range hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, true
}

func safeSSHCompanionSegment(seg, cwd, worktree string, piped bool) (string, bool) {
	if isPathPrependExport(seg) || isBenignPathExport(seg) {
		return cwd, true
	}
	words := stripShellKeywords(shellWords(seg))
	if len(words) == 0 {
		return cwd, true
	}
	if isHarmlessSet(words) {
		return cwd, true
	}
	if filepath.Base(normalizeAbsoluteTool(words[0])) == "cd" {
		nextCWD, ok := advanceScopedCD(seg, cwd, worktree)
		return nextCWD, ok
	}
	if isDestructiveCommand(seg) || isOpaqueShellCommandWith(seg, cwd, scriptTrustNone) {
		return cwd, false
	}
	clean, ok := stripNullRedirects(words)
	if !ok || len(clean) == 0 {
		return cwd, false
	}
	base := filepath.Base(normalizeAbsoluteTool(clean[0]))
	if base == "tee" && piped {
		return cwd, isSafeSSHTempTeeArgs(clean[1:])
	}
	if base == "sed" {
		return cwd, isReadOnlySedArgs(clean[1:])
	}
	if awkTools[base] {
		return cwd, !awkProgramExecutes(base, clean[1:])
	}
	return cwd, readOnlyDataTools[base]
}

func isSafeSSHTempTeeArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	hasTarget := false
	for _, a := range args {
		if a == "-a" || a == "--append" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			return false
		}
		if !isSafeSSHTempLogTarget(a) {
			return false
		}
		hasTarget = true
	}
	return hasTarget
}
