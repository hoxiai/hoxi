package runner

import "github.com/hoxiai/hoxi/internal/canonical"

// NativeTodoItem transfers a persisted todowrite item across the runner-engine boundary.
type NativeTodoItem = canonical.TodoItem

// NativeMessage represents one canonical message passed into NativeEngine.
type NativeMessage = canonical.Message

// NativeContentBlock represents one content block inside NativeMessage.
type NativeContentBlock = canonical.ContentBlock

func canonicalIsMutatingTool(toolName string, input map[string]interface{}) bool {
	return canonical.IsMutatingToolCall(toolName, input)
}

func canonicalStringMeta(meta map[string]interface{}, key string) string {
	return canonical.StringMeta(meta, key)
}

func canonicalIntMeta(meta map[string]interface{}, key string) int {
	return canonical.IntMeta(meta, key)
}

func canonicalAppendMessage(msgs []NativeMessage, role string, blocks []NativeContentBlock) []NativeMessage {
	return canonical.AppendMessage(msgs, role, blocks)
}

func canonicalKeepReasoningAffinity(
	turnProviderID, turnModelID, partText string,
	partMeta map[string]interface{},
	targetProviderID, targetModelID string,
) (string, bool) {
	return canonical.KeepReasoningAffinity(turnProviderID, turnModelID, partText, partMeta, targetProviderID, targetModelID)
}

func canonicalSameProducerModel(turnProviderID, turnModelID, targetProviderID, targetModelID string) bool {
	return canonical.SameProducerModel(turnProviderID, turnModelID, targetProviderID, targetModelID)
}
