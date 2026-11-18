package dataprofiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	defaultSSHTimeout = 25 * time.Second
	maxSSHOutputBytes = 64 * 1024
)

func (s *Service) invokeSSHExec(ctx context.Context, prof Profile, req InvokeRequest) (map[string]any, error) {
	if prof.Kind != KindSSH {
		return nil, fmt.Errorf("profile %q is kind %q, not ssh", prof.ID, prof.Kind)
	}
	cmdStr := strings.TrimSpace(req.Command)
	if cmdStr == "" {
		return nil, errors.New("command is required for ssh_exec")
	}
	if prof.ReadOnly {
		if ok, reason := IsReadOnlySSHCommand(cmdStr); !ok {
			return nil, fmt.Errorf("profile %q is configured as read-only: %s", prof.ID, reason)
		}
	}

	sshBin, err := s.resolveSSHBinary()
	if err != nil {
		return nil, err
	}
	host := strings.TrimSpace(prof.Host)
	if host == "" {
		return nil, fmt.Errorf("ssh profile %q has no host configured", prof.ID)
	}
	if strings.HasPrefix(host, "-") {
		return nil, fmt.Errorf("invalid ssh host %q", host)
	}
	port := prof.Port
	if port <= 0 {
		port = 22
	}

	var tempKeyPath string
	if strings.TrimSpace(prof.PrivateKey) != "" {
		f, fErr := os.CreateTemp("", "hoxi-sshkey-*")
		if fErr != nil {
			return nil, fmt.Errorf("failed to allocate ephemeral SSH key file: %w", fErr)
		}
		tempKeyPath = f.Name()
		_ = f.Chmod(0o600)
		keyContent := strings.TrimSpace(prof.PrivateKey) + "\n"
		_, _ = f.WriteString(keyContent)
		_ = f.Close()
		defer func() { _ = os.Remove(tempKeyPath) }()
	}

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", strconv.Itoa(port),
	}
	if tempKeyPath != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", tempKeyPath)
	} else if kp := strings.TrimSpace(prof.KeyPath); kp != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", kp)
	}
	if user := strings.TrimSpace(prof.Username); user != "" {
		if strings.HasPrefix(user, "-") {
			return nil, fmt.Errorf("invalid ssh username %q", user)
		}
		args = append(args, "-l", user)
	}
	args = append(args, "--", host, cmdStr)

	execCtx, cancel := withDefaultTimeout(ctx, defaultSSHTimeout)
	defer cancel()

	cmd := subproc.CommandContext(execCtx, sshBin, args...)
	env := sanitizedDBEnv()
	env = append(env, "SSH_ASKPASS=", "DISPLAY=")
	if sock := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK")); sock != "" && tempKeyPath == "" && strings.TrimSpace(prof.KeyPath) == "" {
		env = append(env, "SSH_AUTH_SOCK="+sock)
	}
	cmd.Env = env

	stdout, stderr, runErr := runBoundedCmd(cmd, maxSSHOutputBytes)
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("ssh_exec on %s failed: %s", prof.ID, redactProfileSecrets(runErr.Error(), prof))
		}
	}

	cleanOut := redactProfileSecrets(stdout, prof)
	cleanErr := redactProfileSecrets(stderr, prof)
	truncated := len(stdout) >= maxSSHOutputBytes || len(stderr) >= maxSSHOutputBytes
	res := map[string]any{
		"profile":   prof.ID,
		"kind":      KindSSH,
		"exitCode":  exitCode,
		"stdout":    cleanOut,
		"stderr":    cleanErr,
		"truncated": truncated,
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(cleanErr)
		if msg == "" {
			msg = strings.TrimSpace(cleanOut)
		}
		if msg == "" {
			msg = fmt.Sprintf("remote command exited with code %d", exitCode)
		}
		return res, fmt.Errorf("ssh_exec on %s exited with %d: %s", prof.ID, exitCode, msg)
	}
	return res, nil
}

func (s *Service) resolveSSHBinary() (string, error) {
	if s.sshBin != "" {
		return s.sshBin, nil
	}
	bin, err := exec.LookPath("ssh")
	if err != nil || bin == "" {
		return "", errors.New("ssh CLI is not installed in PATH on this host")
	}
	return bin, nil
}

func runBoundedCmd(cmd *exec.Cmd, maxBytes int) (stdout, stderr string, err error) {
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = outPipe.Close()
		return "", "", err
	}
	if err := cmd.Start(); err != nil {
		_ = outPipe.Close()
		_ = errPipe.Close()
		return "", "", err
	}
	var outBuf, errBuf bytes.Buffer
	doneOut := make(chan struct{})
	doneErr := make(chan struct{})
	go func() {
		_, _ = io.Copy(&outBuf, io.LimitReader(outPipe, int64(maxBytes)))
		_, _ = io.Copy(io.Discard, outPipe)
		close(doneOut)
	}()
	go func() {
		_, _ = io.Copy(&errBuf, io.LimitReader(errPipe, int64(maxBytes)))
		_, _ = io.Copy(io.Discard, errPipe)
		close(doneErr)
	}()
	<-doneOut
	<-doneErr
	waitErr := cmd.Wait()
	return outBuf.String(), errBuf.String(), waitErr
}

func redactProfileSecrets(text string, prof Profile) string {
	out := text
	for _, sec := range []string{prof.BearerToken, prof.Password, prof.PrivateKey} {
		out = redactSecret(out, sec)
	}
	for _, hdrVal := range prof.Headers {
		out = redactSecret(out, hdrVal)
	}
	return out
}
