package sandbox

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// seatbeltBase is copied from Kilo Code packages/kilo-sandbox/src/seatbelt-base.ts
// so runtime helpers, TTYs and system services keep working under (deny default).
const seatbeltBase = `(version 1)

(deny default)

(allow process-exec)
(allow process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))

(allow file-write-data
  (require-all
    (path "/dev/null")
    (vnode-type CHARACTER-DEVICE)))

(allow sysctl-read
  (sysctl-name "hw.activecpu")
  (sysctl-name "hw.busfrequency_compat")
  (sysctl-name "hw.byteorder")
  (sysctl-name "hw.cacheconfig")
  (sysctl-name "hw.cachelinesize_compat")
  (sysctl-name "hw.cpufamily")
  (sysctl-name "hw.cpufrequency_compat")
  (sysctl-name "hw.cputype")
  (sysctl-name "hw.l1dcachesize_compat")
  (sysctl-name "hw.l1icachesize_compat")
  (sysctl-name "hw.l2cachesize_compat")
  (sysctl-name "hw.l3cachesize_compat")
  (sysctl-name "hw.logicalcpu_max")
  (sysctl-name "hw.machine")
  (sysctl-name "hw.model")
  (sysctl-name "hw.memsize")
  (sysctl-name "hw.ncpu")
  (sysctl-name "hw.nperflevels")
  (sysctl-name-prefix "hw.optional.arm.")
  (sysctl-name-prefix "hw.optional.armv8_")
  (sysctl-name "hw.packages")
  (sysctl-name "hw.pagesize_compat")
  (sysctl-name "hw.pagesize")
  (sysctl-name "hw.physicalcpu")
  (sysctl-name "hw.physicalcpu_max")
  (sysctl-name "hw.logicalcpu")
  (sysctl-name "hw.cpufrequency")
  (sysctl-name "hw.tbfrequency_compat")
  (sysctl-name "hw.vectorunit")
  (sysctl-name "machdep.cpu.brand_string")
  (sysctl-name "kern.argmax")
  (sysctl-name "kern.hostname")
  (sysctl-name "kern.maxfilesperproc")
  (sysctl-name "kern.maxproc")
  (sysctl-name "kern.osproductversion")
  (sysctl-name "kern.osrelease")
  (sysctl-name "kern.ostype")
  (sysctl-name "kern.osvariant_status")
  (sysctl-name "kern.osversion")
  (sysctl-name "kern.secure_kernel")
  (sysctl-name "kern.usrstack64")
  (sysctl-name "kern.version")
  (sysctl-name "sysctl.proc_cputype")
  (sysctl-name "vm.loadavg")
  (sysctl-name-prefix "hw.perflevel")
  (sysctl-name-prefix "kern.proc.pgrp.")
  (sysctl-name-prefix "kern.proc.pid.")
  (sysctl-name-prefix "net.routetable.")
)

(allow sysctl-write
  (sysctl-name "kern.grade_cputype"))

(allow iokit-open
  (iokit-registry-entry-class "RootDomainUserClient"))

(allow mach-lookup
  (global-name "com.apple.system.opendirectoryd.libinfo"))

(allow ipc-posix-sem)

(allow ipc-posix-shm-read-data
  ipc-posix-shm-write-create
  ipc-posix-shm-write-unlink
  (ipc-posix-name-regex #"^/__KMP_REGISTERED_LIB_[0-9]+$"))

(allow mach-lookup
  (global-name "com.apple.PowerManagement.control"))

(allow pseudo-tty)
(allow file-read* file-write* file-ioctl (literal "/dev/ptmx"))
(allow file-read* file-write*
  (require-all
    (regex #"^/dev/ttys[0-9]+")
    (extension "com.apple.sandbox.pty")))
(allow file-ioctl (regex #"^/dev/ttys[0-9]+"))

(allow ipc-posix-shm-read* (ipc-posix-name-prefix "apple.cfprefs."))
(allow mach-lookup
  (global-name "com.apple.cfprefsd.daemon")
  (global-name "com.apple.cfprefsd.agent")
  (local-name "com.apple.cfprefsd.agent"))
(allow user-preference-read)

; system services required by common command-line runtimes
(allow system-socket (require-all (socket-domain AF_SYSTEM) (socket-protocol 2)))
(allow mach-lookup
  (global-name "com.apple.bsd.dirhelper")
  (global-name "com.apple.system.opendirectoryd.membership")
  (global-name "com.apple.SecurityServer")
  (global-name "com.apple.networkd")
  (global-name "com.apple.ocspd")
  (global-name "com.apple.trustd.agent")
  (global-name "com.apple.SystemConfiguration.DNSConfiguration")
  (global-name "com.apple.SystemConfiguration.configd"))
(allow sysctl-read (sysctl-name-regex #"^net.routetable"))
`

// Available reports whether OS-level confinement can be enforced here.
func Available() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	st, err := os.Stat(sandboxExec)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func UnavailableReason() string {
	if runtime.GOOS != "darwin" {
		return "the OS sandbox backend is only available on macOS"
	}
	return sandboxExec + " is not available"
}

// ProfileText renders a Seatbelt profile equivalent to Kilo Code's
// seatbelt policy generator.
func ProfileText(profile Profile) (string, error) {
	if len(profile.Filesystem.AllowWrite) == 0 {
		return "", errors.New("sandbox profile requires at least one writable path")
	}
	var allow []string
	for _, rule := range profile.Filesystem.AllowWrite {
		filter, err := pathFilter(rule)
		if err != nil {
			return "", err
		}
		allow = append(allow, filter)
	}
	var denyRead []string
	for _, rule := range profile.Filesystem.DenyRead {
		filter, err := pathFilter(rule)
		if err != nil {
			return "", err
		}
		denyRead = append(denyRead, filter)
	}
	read := "(allow file-read*)"
	if len(denyRead) > 0 {
		read = "(deny file-read*\n  (require-any " + strings.Join(denyRead, " ") + ")\n)\n(allow file-read*)"
	}
	var deny []string
	for _, rule := range profile.Filesystem.DenyWrite {
		filter, err := pathFilter(rule)
		if err != nil {
			return "", err
		}
		deny = append(deny, "(require-not "+filter+")")
	}
	var exempt []string
	for _, rule := range profile.Filesystem.NameDenyExemptions {
		filter, err := pathFilter(rule)
		if err != nil {
			return "", err
		}
		exempt = append(exempt, filter)
	}
	for _, name := range profile.Filesystem.DenyNames {
		if strings.TrimSpace(name) == "" {
			continue
		}
		filter := `(regex #"(^|/)` + escapeRegex(name) + `(/|$)")`
		if len(exempt) > 0 {
			filter = "(require-all " + filter + " (require-not (require-any " + strings.Join(exempt, " ") + ")))"
		}
		deny = append(deny, "(require-not "+filter+")")
	}
	write := ""
	if len(allow) > 0 {
		write = "(allow file-write*\n  (require-all\n    (require-any " +
			strings.Join(allow, " ") + ")\n    " + strings.Join(deny, "\n    ") + "\n  )\n)"
	}
	mode := profile.Network.Mode
	if mode == "" {
		mode = NetworkDeny
	}
	network := ""
	switch mode {
	case NetworkAllow:
		network = "; sandbox network mode: allow\n(allow network-outbound)\n(allow network-inbound)"
	case NetworkDeny:
		network = "; sandbox network mode: deny\n" +
			`(deny network-outbound (with message "Hoxi sandbox denied outbound network access"))` + "\n" +
			"(allow network-inbound)"
	default:
		return "", fmt.Errorf("unsupported sandbox network mode: %s", mode)
	}
	return strings.Join([]string{
		seatbeltBase,
		network,
		"; credential directories and secret files are unreadable\n" + read,
		write,
	}, "\n"), nil
}

func pathFilter(rule PathRule) (string, error) {
	quoted, err := quoteProfilePath(rule.Path)
	if err != nil {
		return "", err
	}
	if rule.Kind == PathLiteral {
		return "(literal " + quoted + ")", nil
	}
	return "(require-any (literal " + quoted + ") (subpath " + quoted + "))", nil
}

func quoteProfilePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("sandbox path is empty")
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("sandbox path contains a control character: %q", path)
		}
	}
	escaped := strings.ReplaceAll(path, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`, nil
}

func escapeRegex(value string) string {
	var b strings.Builder
	for _, r := range value {
		if strings.ContainsRune(`.*+?()[]{}^$|\`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// WrapShell confines a shell command line, matching Kilo's launch.shell path.
func WrapShell(profile Profile, command string) (string, []string, error) {
	text, err := ProfileText(profile)
	if err != nil {
		return "", nil, err
	}
	args := []string{"-p", text, "--", "/bin/sh", "-c", command}
	return sandboxExec, args, nil
}

// WrapExec confines a directly spawned executable.
func WrapExec(profile Profile, bin string, args []string) (string, []string, error) {
	text, err := ProfileText(profile)
	if err != nil {
		return "", nil, err
	}
	wrapped := []string{"-p", text, "--", bin}
	wrapped = append(wrapped, args...)
	return sandboxExec, wrapped, nil
}
