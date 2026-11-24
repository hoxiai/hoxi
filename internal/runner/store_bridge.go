package runner

import (
	"net/url"
	"time"

	"github.com/hoxiai/hoxi/internal/runner/sessionstore"
)

func init() {
	sessionstore.RegisterTranscriptHooks(sessionstore.TranscriptHooks{
		HydrateEdits:        hydrateAgyTranscriptEditParts,
		HydrateTurnTokens:   hydrateAgyTranscriptTurnTokens,
		PreserveEditInput:   preserveToolEditInput,
		PruneDuplicateTools: pruneRunawayDuplicateToolParts,
	})
}

type (
	RuntimeType          = sessionstore.RuntimeType
	Store                = sessionstore.Store
	SessionRecord        = sessionstore.SessionRecord
	SessionInfo          = sessionstore.SessionInfo
	SessionTime          = sessionstore.SessionTime
	SessionRevert        = sessionstore.SessionRevert
	SessionCapabilities  = sessionstore.SessionCapabilities
	SessionStatus        = sessionstore.SessionStatus
	SessionMetadata      = sessionstore.SessionMetadata
	SessionEditsSnapshot = sessionstore.SessionEditsSnapshot
	HistoryPage          = sessionstore.HistoryPage
	MessageTurn          = sessionstore.MessageTurn
	TurnUsageSummary     = sessionstore.TurnUsageSummary
	MessageInfo          = sessionstore.MessageInfo
	MessageTime          = sessionstore.MessageTime
	MessageTokens        = sessionstore.MessageTokens
	MessageError         = sessionstore.MessageError
	MessageErrorData     = sessionstore.MessageErrorData
	MessagePart          = sessionstore.MessagePart
	PartTime             = sessionstore.PartTime
	ToolState            = sessionstore.ToolState
	SSEEvent             = sessionstore.SSEEvent
)

type partKey struct{ messageID, partID string }

const (
	RuntimeOpenCode    = sessionstore.RuntimeOpenCode
	RuntimeCodex       = sessionstore.RuntimeCodex
	RuntimeAntigravity = sessionstore.RuntimeAntigravity
	RuntimeClaudeCode  = sessionstore.RuntimeClaudeCode
	RuntimeGeminiCLI   = sessionstore.RuntimeGeminiCLI
	RuntimeCodeBuddy   = sessionstore.RuntimeCodeBuddy
	RuntimeQoder       = sessionstore.RuntimeQoder
	RuntimeNative      = sessionstore.RuntimeNative

	sessionMetaFile     = sessionstore.SessionMetaFile
	sessionTurnsFile    = sessionstore.SessionTurnsFile
	legacyStoreFile     = sessionstore.LegacyStoreFile
	maxHistoryPageTurns = sessionstore.MaxHistoryPageTurns
)

func NewStore(customDir string) *Store         { return sessionstore.NewStore(customDir) }
func GenerateRunnerSessionID(rt string) string { return sessionstore.GenerateRunnerSessionID(rt) }
func IsRunnerSessionID(sessionID string) bool  { return sessionstore.IsRunnerSessionID(sessionID) }
func IsValidRuntime(rt string) bool            { return sessionstore.IsValidRuntime(rt) }
func IsClaudeProtocolRuntime(rt string) bool   { return sessionstore.IsClaudeProtocolRuntime(rt) }
func RuntimeCapabilities(rt string, subagent bool) SessionCapabilities {
	return sessionstore.RuntimeCapabilities(rt, subagent)
}
func NormalizeSessionSurface(surface, directory string) string {
	return sessionstore.NormalizeSessionSurface(surface, directory)
}
func MeasureDirBytes(root string) int64 { return sessionstore.MeasureDirBytes(root) }
func PruneTempFiles(root string, minAge time.Duration) (int, int64) {
	return sessionstore.PruneTempFiles(root, minAge)
}

func equalMessageInfo(a, b MessageInfo) bool { return sessionstore.EqualMessageInfo(a, b) }
func equalMessagePart(a, b MessagePart) bool { return sessionstore.EqualMessagePart(a, b) }
func cloneGenericMap(m map[string]interface{}) map[string]interface{} {
	return sessionstore.CloneGenericMap(m)
}
func cloneStringMap(m map[string]string) map[string]string { return sessionstore.CloneStringMap(m) }
func cloneMessageInfo(info MessageInfo) MessageInfo        { return sessionstore.CloneMessageInfo(info) }
func clonePart(p MessagePart) MessagePart                  { return sessionstore.ClonePart(p) }
func cloneTurns(turns []MessageTurn) []MessageTurn         { return sessionstore.CloneTurns(turns) }
func cloneRecord(r SessionRecord) SessionRecord            { return sessionstore.CloneRecord(r) }
func normalizeComparePath(p string) string                 { return sessionstore.NormalizeComparePath(p) }
func validStoreID(id string) bool                          { return sessionstore.ValidStoreID(id) }
func writeFileAtomic(path string, raw []byte) error        { return sessionstore.WriteFileAtomic(path, raw) }
func sessionMatchesDirectory(rec SessionRecord, normFilter string) bool {
	return sessionstore.SessionMatchesDirectory(rec, normFilter)
}
func parseHistoryPage(q url.Values) HistoryPage { return sessionstore.ParseHistoryPage(q) }
func sliceHistoryPage(turns []MessageTurn, page HistoryPage) []MessageTurn {
	return sessionstore.SliceHistoryPage(turns, page)
}
func isRunnerEditingTool(tool string) bool { return sessionstore.IsRunnerEditingTool(tool) }
func isRunnerTaskTool(tool string) bool    { return sessionstore.IsRunnerTaskTool(tool) }
func extractChildSessionIDsFromPart(part MessagePart) []string {
	return sessionstore.ExtractChildSessionIDsFromPart(part)
}
func extractPatchTextPaths(patchText string) []string {
	return sessionstore.ExtractPatchTextPaths(patchText)
}
func extractEditedPathsFromPart(part MessagePart) []string {
	return sessionstore.ExtractEditedPathsFromPart(part)
}
func normalizeSessionRelPath(raw, projectDir, worktreeDir string) string {
	return sessionstore.NormalizeSessionRelPath(raw, projectDir, worktreeDir)
}
func matchesEditedFileSet(candidate, projectDir, worktreeDir string, edited map[string]struct{}) bool {
	return sessionstore.MatchesEditedFileSet(candidate, projectDir, worktreeDir, edited)
}
