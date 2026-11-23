package runner

import (
	"fmt"
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/runner/taskledger"
)

// PromptOrchestrator configures dual-model Orchestrator + Executor execution for
// a native Go Agent turn. The turn's primary Model acts as the Orchestrator
// (planner/reviewer), while write/shell/heavy subtasks run on the Executor model.
type PromptOrchestrator struct {
	ExecutorProviderID string `json:"executorProviderID"`
	ExecutorModelID    string `json:"executorModelID"`
	ExecutorEffort     string `json:"executorEffort,omitempty"`
}

// NativeOrchestratorConfig carries validated Executor settings into NativeEngine.
type NativeOrchestratorConfig struct {
	ExecutorProviderID string
	ExecutorModelID    string
	ExecutorEffort     string
}

func normalizePromptOrchestrator(in *PromptOrchestrator) (*PromptOrchestrator, error) {
	if in == nil {
		return nil, nil
	}
	pid := strings.TrimSpace(in.ExecutorProviderID)
	mid := strings.TrimSpace(in.ExecutorModelID)
	if pid == "" || mid == "" {
		return nil, fmt.Errorf("orchestrator requires both executorProviderID and executorModelID")
	}
	if strings.ContainsAny(pid, " \t\r\n") || strings.ContainsAny(mid, " \t\r\n") {
		return nil, fmt.Errorf("orchestrator executor model must not contain whitespace")
	}
	if pid == "hoxi-auto" || pid == "hoxi-orchestrator" {
		return nil, fmt.Errorf("orchestrator executor cannot use virtual provider %q", pid)
	}
	if IsValidRuntime(pid) || pid == "opencode" {
		return nil, fmt.Errorf("orchestrator executor must use a native API provider, not runtime %q", pid)
	}
	rawEffort := strings.TrimSpace(in.ExecutorEffort)
	eff := ""
	if rawEffort != "" {
		eff = agentcli.NormalizeEffortName(rawEffort)
		if eff == "" {
			return nil, fmt.Errorf("unsupported orchestrator executor effort %q", in.ExecutorEffort)
		}
	}
	return &PromptOrchestrator{
		ExecutorProviderID: pid,
		ExecutorModelID:    mid,
		ExecutorEffort:     eff,
	}, nil
}

func toLedgerOrchestrator(in *PromptOrchestrator) *taskledger.OrchestratorSpec {
	if in == nil {
		return nil
	}
	return &taskledger.OrchestratorSpec{
		ExecutorProviderID: in.ExecutorProviderID,
		ExecutorModelID:    in.ExecutorModelID,
		ExecutorEffort:     in.ExecutorEffort,
	}
}

func fromLedgerOrchestrator(in *taskledger.OrchestratorSpec) *NativeOrchestratorConfig {
	if in == nil || strings.TrimSpace(in.ExecutorProviderID) == "" || strings.TrimSpace(in.ExecutorModelID) == "" {
		return nil
	}
	return &NativeOrchestratorConfig{
		ExecutorProviderID: strings.TrimSpace(in.ExecutorProviderID),
		ExecutorModelID:    strings.TrimSpace(in.ExecutorModelID),
		ExecutorEffort:     strings.TrimSpace(in.ExecutorEffort),
	}
}

func toNativeOrchestrator(in *PromptOrchestrator) *NativeOrchestratorConfig {
	if in == nil {
		return nil
	}
	return &NativeOrchestratorConfig{
		ExecutorProviderID: in.ExecutorProviderID,
		ExecutorModelID:    in.ExecutorModelID,
		ExecutorEffort:     in.ExecutorEffort,
	}
}

func validatePromptOrchestratorForRuntime(in *PromptOrchestrator, runtime string) (*PromptOrchestrator, error) {
	if in == nil {
		return nil, nil
	}
	spec, err := normalizePromptOrchestrator(in)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(runtime) != string(RuntimeNative) {
		return nil, fmt.Errorf("orchestrator mode is only supported on the native Go runtime (got %q)", runtime)
	}
	return spec, nil
}

func (s *Service) forgetNativeSessionState(sessionID string) {
	trimmed := strings.TrimSpace(sessionID)
	if s == nil || trimmed == "" {
		return
	}
	s.mu.Lock()
	eng := s.nativeEngine
	s.mu.Unlock()
	if cleaner, ok := eng.(interface{ ForgetSession(string) }); ok {
		cleaner.ForgetSession(trimmed)
	}
}
