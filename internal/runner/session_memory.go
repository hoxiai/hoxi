package runner

import "github.com/hoxiai/hoxi/internal/runner/memstore"

const MaxMemoryDocumentBytes = memstore.MaxMemoryDocumentBytes

var (
	ErrNoPromotableRules = memstore.ErrNoPromotableRules
	ErrInvalidMemoryPath = memstore.ErrInvalidMemoryPath
)

func SanitizeMemorySessionID(raw string) string {
	return memstore.SanitizeMemorySessionID(raw)
}

func SaveMemoryDocument(projectRoot, scope, sessionID, content string) (string, string, error) {
	return memstore.SaveMemoryDocument(projectRoot, scope, sessionID, content)
}

func PromoteSessionMemoryToProject(projectRoot, sessionID, sessionContent string) (string, string, int, error) {
	return memstore.PromoteSessionMemoryToProject(projectRoot, sessionID, sessionContent)
}

func MergeSessionMemoryMarkdown(existing, autoMD string) string {
	return memstore.MergeSessionMemoryMarkdown(existing, autoMD)
}

func (s *Service) syncSessionMemoryFile(sessionID string) {
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	memstore.SyncSessionMemoryRecord(rec)
}

func redactMemorySecrets(text string) string { return memstore.RedactMemorySecrets(text) }
func summarizeUserMessageExcluding(txt string, maxRunes int, exclude map[string]bool) string {
	return memstore.SummarizeUserMessageExcluding(txt, maxRunes, exclude)
}
func truncateUTF8Bytes(s string, maxBytes int) string { return memstore.TruncateUTF8Bytes(s, maxBytes) }
func formatHandoffConstraints(constraints []string, maxBytes int) (string, map[string]bool) {
	return memstore.FormatHandoffConstraints(constraints, maxBytes)
}
func collectSessionConstraints(turns []MessageTurn) []string {
	return memstore.CollectSessionConstraints(turns)
}
func isMutatingToolName(tool string) bool { return memstore.IsMutatingToolName(tool) }
func extractToolLocationQualifier(input map[string]interface{}) string {
	return memstore.ExtractToolLocationQualifier(input)
}
func stripVolatileMemoryHeader(md string) string  { return memstore.StripVolatileMemoryHeader(md) }
func dedupHandoffMemorySection6(md string) string { return memstore.DedupHandoffMemorySection6(md) }
func memoryCandidates(rec SessionRecord, rel string) []string {
	return memstore.MemoryCandidates(rec, rel)
}
func runtimeMemoryContext(rec SessionRecord) string { return memstore.RuntimeMemoryContext(rec) }
func buildEightDimSessionMarkdown(rec SessionRecord) string {
	return memstore.BuildEightDimSessionMarkdown(rec)
}
func singleLineExcerpt(s string, maxRunes int) string { return memstore.SingleLineExcerpt(s, maxRunes) }
func readFirstNonEmptyFile(paths ...string) string    { return memstore.ReadFirstNonEmptyFile(paths...) }
func isAgyBrainMemoryLine(trimmedLine string) bool    { return memstore.IsAgyBrainMemoryLine(trimmedLine) }
func sanitizeSessionMemoryMarkdown(md string) string {
	return memstore.SanitizeSessionMemoryMarkdown(md)
}
