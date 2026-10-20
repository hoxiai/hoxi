package gateway

import (
	"github.com/hoxiai/hoxi/internal/promptassist"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/worktree"
)

// wireAssistRoutes injects the provider's LLM client into the domain packages
// that own each AI helper, and mounts their handlers behind the historical
// /api/hoxi/provider/<action> routes so the public contract is unchanged.
func wireAssistRoutes(providers *provider.Service, worktrees *worktree.Manager) {
	worktrees.SetReviewLLM(providers, providers.RecordAuditResult)
	assist := promptassist.NewService(providers)
	providers.SetAssistHandler(provider.AssistAudit, worktrees.HandleAudit)
	providers.SetAssistHandler(provider.AssistGenerateCommitMessage, worktrees.HandleGenerateCommitMessage)
	providers.SetAssistHandler(provider.AssistSummarizeTitle, assist.HandleSummarizeTitle)
	providers.SetAssistHandler(provider.AssistEnhancePrompt, assist.HandleEnhancePrompt)
}
