package runner

import "github.com/hoxiai/hoxi/internal/runner/turnstream"

type (
	TurnTranslator   = turnstream.TurnTranslator
	ChildSessionSpec = turnstream.ChildSessionSpec
)

const (
	agyHookName            = turnstream.AgyHookName
	interruptedToolError   = turnstream.InterruptedToolError
	questionAnsweredPrefix = turnstream.QuestionAnsweredPrefix
)

func NewTurnTranslator(sessionID, runtime, messageID, parentUserMsgID, providerID, modelID, mode, externalID string) *TurnTranslator {
	return turnstream.NewTurnTranslator(sessionID, runtime, messageID, parentUserMsgID, providerID, modelID, mode, externalID)
}

func isInFlightToolPart(p MessagePart) bool { return turnstream.IsInFlightToolPart(p) }
func newClassifiedMessageError(name, message string, status int) *MessageError {
	return turnstream.NewClassifiedMessageError(name, message, status)
}

func isQuestionToolName(name string) bool { return turnstream.IsQuestionToolName(name) }
func hasQuestionsInput(input map[string]interface{}) bool {
	return turnstream.HasQuestionsInput(input)
}
func normalizeRunnerQuestions(rawArgs map[string]interface{}) []map[string]interface{} {
	return turnstream.NormalizeRunnerQuestions(rawArgs)
}
func normalizeQuestionToolResult(normTool string, status *string, output, errMsg *string) {
	turnstream.NormalizeQuestionToolResult(normTool, status, output, errMsg)
}

func firstString(vals ...interface{}) string            { return turnstream.FirstString(vals...) }
func intFromMap(m map[string]interface{}, k string) int { return turnstream.IntFromMap(m, k) }
func agyBrainRoots() []string                           { return turnstream.AgyBrainRoots() }
func agyTranscriptLogDir(root, externalID string) string {
	return turnstream.AgyTranscriptLogDir(root, externalID)
}
func decodeAgyToolArgs(raw map[string]interface{}) map[string]interface{} {
	return turnstream.DecodeAgyToolArgs(raw)
}
func eventSessionID(evt SSEEvent) string { return turnstream.EventSessionID(evt) }
func geminiErrorText(raw []byte) string  { return turnstream.GeminiErrorText(raw) }
func hasAgySplitBrainConversation(externalID string) bool {
	return turnstream.HasAgySplitBrainConversation(externalID)
}
func hydrateAgyTranscriptEditParts(externalID string, turns []MessageTurn) bool {
	return turnstream.HydrateAgyTranscriptEditParts(externalID, turns)
}
func hydrateAgyTranscriptTurnTokens(externalID string, turns []MessageTurn) bool {
	return turnstream.HydrateAgyTranscriptTurnTokens(externalID, turns)
}
func lookupAgyTranscriptThinking(externalID string, stepIndex int) string {
	return turnstream.LookupAgyTranscriptThinking(externalID, stepIndex)
}
func partUpdated(part MessagePart) SSEEvent { return turnstream.PartUpdated(part) }
func preserveToolEditInput(incoming *MessagePart, existing MessagePart) {
	turnstream.PreserveToolEditInput(incoming, existing)
}
func quarantineEmptyAgySQLiteStore(externalID string) bool {
	return turnstream.QuarantineEmptyAgySQLiteStore(externalID)
}
