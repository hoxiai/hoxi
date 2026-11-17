package runner

import (
	"fmt"
	"strings"
	"sync"
)

// nativeSubagentBridge maps in-process NativeEngine subagent events (`task`)
// onto Hoxi child sessions (`TurnTranslator.openChild` / `childSink`) so the
// web & mobile timeline UI renders live subagent step counts, current tool
// labels, resolved child model metadata, inline expandable step drawers, and
// AgentStackPill controls.
type nativeSubagentBridge struct {
	svc             *Service
	parentSessionID string
	modelID         string
	tr              *TurnTranslator

	mu               sync.Mutex
	taskParts        map[string]MessagePart    // parent CallID -> latest task MessagePart
	lastActByID      map[string]int64          // parent CallID -> latest activity ms
	childMetaByID    map[string]map[string]any // parent CallID -> resolved child model/role metadata
	childMutatedByID map[string]bool           // parent CallID -> whether child started/ran a mutating tool
}

func newNativeSubagentBridge(svc *Service, parentSessionID, modelID string, tr *TurnTranslator) *nativeSubagentBridge {
	return &nativeSubagentBridge{
		svc:              svc,
		parentSessionID:  parentSessionID,
		modelID:          modelID,
		tr:               tr,
		taskParts:        make(map[string]MessagePart),
		lastActByID:      make(map[string]int64),
		childMetaByID:    make(map[string]map[string]any),
		childMutatedByID: make(map[string]bool),
	}
}

func metaStr(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	v, _ := meta[key].(string)
	return strings.TrimSpace(v)
}

func applyChildModelMeta(dst map[string]interface{}, childMeta map[string]any) {
	if dst == nil || len(childMeta) == 0 {
		return
	}
	for _, k := range []string{"subagent_type", "providerID", "modelID", "model", "routing"} {
		if v := metaStr(childMeta, k); v != "" {
			dst[k] = v
		}
	}
	if ro, ok := childMeta["readOnly"].(bool); ok {
		dst["readOnly"] = ro
	}
}

// enrichParentTaskPart attaches child session linkage (`sessionId`, `runner`,
// `lastActivityAt`, and resolved child model metadata) to a parent `task` tool
// part before broadcasting.
func (b *nativeSubagentBridge) enrichParentTaskPart(part *MessagePart, nowMs int64) {
	if b == nil || part == nil || part.State == nil || !strings.EqualFold(strings.TrimSpace(part.Tool), "task") {
		return
	}
	callID := strings.TrimSpace(part.CallID)
	if callID == "" {
		return
	}
	b.tr.mu.Lock()
	childID := b.tr.childSessionID(callID)
	b.tr.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	if part.State.Metadata == nil {
		part.State.Metadata = make(map[string]interface{}, 6)
	}
	part.State.Metadata["runner"] = string(RuntimeNative)
	if act := b.lastActByID[callID]; act > 0 {
		part.State.Metadata["lastActivityAt"] = act
	} else {
		part.State.Metadata["lastActivityAt"] = nowMs
		b.lastActByID[callID] = nowMs
	}
	if childID != "" {
		part.State.Metadata["sessionId"] = childID
	}
	applyChildModelMeta(part.State.Metadata, b.childMetaByID[callID])
	if b.childMutatedByID[callID] {
		part.State.Metadata["mutated"] = true
	}
	b.taskParts[callID] = clonePart(*part)
}

// handleChildEvent processes a NativeEvent tagged with a non-empty ParentCallID.
func (b *nativeSubagentBridge) handleChildEvent(evt NativeEvent, nowMs int64) []SSEEvent {
	if b == nil || b.tr == nil {
		return nil
	}
	parentCallID := strings.TrimSpace(evt.ParentCallID)
	if parentCallID == "" {
		return nil
	}

	if evt.Kind == NativeEventSubtaskStart && len(evt.Metadata) > 0 {
		b.mu.Lock()
		copied := make(map[string]any, len(evt.Metadata))
		for k, v := range evt.Metadata {
			copied[k] = v
		}
		b.childMetaByID[parentCallID] = copied
		b.mu.Unlock()
	}
	if (evt.Kind == NativeEventToolRunning || evt.Kind == NativeEventToolCompleted || evt.Kind == NativeEventToolError) &&
		isMutatingRunnerTool(evt.ToolName, evt.Input) {
		b.mu.Lock()
		b.childMutatedByID[parentCallID] = true
		b.mu.Unlock()
	} else if evt.Kind == NativeEventSubtaskEnd {
		if mut, ok := evt.Metadata["mutated"].(bool); ok && mut {
			b.mu.Lock()
			b.childMutatedByID[parentCallID] = true
			b.mu.Unlock()
		}
	}

	b.tr.mu.Lock()
	var evts []SSEEvent
	switch evt.Kind {
	case NativeEventSubtaskStart:
		childProv := metaStr(evt.Metadata, "providerID")
		childModel := firstNonEmptyStr(metaStr(evt.Metadata, "modelID"), b.modelID)
		childMode := firstNonEmptyStr(metaStr(evt.Metadata, "mode"), "plan")
		subType := metaStr(evt.Metadata, "subagent_type")
		evts = append(evts, b.tr.openChild(parentCallID, ChildSessionSpec{
			Title:      firstNonEmptyStr(evt.Title, "Subagent"),
			Prompt:     evt.Text,
			ProviderID: childProv,
			ModelID:    childModel,
			Mode:       childMode,
			Agent:      subType,
		}, nowMs)...)

	case NativeEventThinkingEnd:
		if c := b.tr.children[parentCallID]; c != nil {
			suffix := firstNonEmptyStr(evt.PartID, fmt.Sprintf("think_%d", nowMs))
			evts = append(evts, c.upsertText("reasoning", suffix, evt.Text, nowMs, nowMs)...)
		}

	case NativeEventTextEnd:
		if c := b.tr.children[parentCallID]; c != nil {
			suffix := firstNonEmptyStr(evt.PartID, fmt.Sprintf("text_%d", nowMs))
			evts = append(evts, c.upsertText("text", suffix, evt.Text, nowMs, nowMs)...)
		}

	case NativeEventToolRunning:
		if c := b.tr.children[parentCallID]; c != nil {
			key := firstNonEmptyStr(evt.CallID, evt.PartID, fmt.Sprintf("tool_%d", nowMs))
			evts = append(evts, c.startTool(key, evt.CallID, evt.ToolName, evt.Input, nowMs)...)
		}

	case NativeEventToolCompleted:
		if c := b.tr.children[parentCallID]; c != nil {
			evts = append(evts, c.finishTool(evt.CallID, evt.Text, false, nowMs)...)
		}

	case NativeEventToolError:
		if c := b.tr.children[parentCallID]; c != nil {
			evts = append(evts, c.finishTool(evt.CallID, firstNonEmptyStr(evt.ErrorText, evt.Text), true, nowMs)...)
		}

	case NativeEventSubtaskEnd:
		evts = append(evts, b.tr.closeChild(parentCallID, strings.TrimSpace(evt.ErrorText) != "", nowMs)...)
	}
	childID := b.tr.childSessionID(parentCallID)
	b.tr.mu.Unlock()

	// Update parent `task` tool part metadata (`sessionId`, `lastActivityAt`,
	// and resolved child model/role) so running ToolCard shimmer & details stay live.
	b.mu.Lock()
	b.lastActByID[parentCallID] = nowMs
	childMeta := b.childMetaByID[parentCallID]
	if stored, ok := b.taskParts[parentCallID]; ok && stored.State != nil {
		cp := clonePart(stored)
		if cp.State.Metadata == nil {
			cp.State.Metadata = make(map[string]interface{}, 6)
		}
		cp.State.Metadata["runner"] = string(RuntimeNative)
		cp.State.Metadata["lastActivityAt"] = nowMs
		if childID != "" {
			cp.State.Metadata["sessionId"] = childID
		}
		applyChildModelMeta(cp.State.Metadata, childMeta)
		if b.childMutatedByID[parentCallID] {
			cp.State.Metadata["mutated"] = true
		}
		b.taskParts[parentCallID] = cp
		if evt.Kind == NativeEventSubtaskStart || evt.Kind == NativeEventToolRunning ||
			evt.Kind == NativeEventToolCompleted || evt.Kind == NativeEventToolError || evt.Kind == NativeEventSubtaskEnd {
			evts = append(evts, partUpdated(cp))
		}
	}
	b.mu.Unlock()

	return evts
}

// abandonAll settles any unfinished child sessions if the parent turn aborts or errors.
func (b *nativeSubagentBridge) abandonAll(nowMs int64) []SSEEvent {
	if b == nil || b.tr == nil {
		return nil
	}
	return b.tr.AbandonTurn(nowMs)
}
