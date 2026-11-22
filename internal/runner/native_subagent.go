package runner

import (
	"fmt"
	"strings"
	"sync"
)

const nativeSubagentHeartbeatIntervalMs int64 = 5000

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

	mu                sync.Mutex
	taskParts         map[string]MessagePart    // parent CallID -> latest task MessagePart
	lastActByID       map[string]int64          // parent CallID -> latest activity ms
	lastParentEmitAt  map[string]int64          // parent CallID -> last time parent task part was re-broadcast
	lastParentMutated map[string]bool           // parent CallID -> last mutated flag broadcast on parent task part
	lastParentChildID map[string]string         // parent CallID -> last child sessionId broadcast on parent task part
	childMetaByID     map[string]map[string]any // parent CallID -> resolved child model/role metadata
	childMutatedByID  map[string]bool           // parent CallID -> whether child started/ran a mutating tool
}

func newNativeSubagentBridge(svc *Service, parentSessionID, modelID string, tr *TurnTranslator) *nativeSubagentBridge {
	return &nativeSubagentBridge{
		svc:               svc,
		parentSessionID:   parentSessionID,
		modelID:           modelID,
		tr:                tr,
		taskParts:         make(map[string]MessagePart),
		lastActByID:       make(map[string]int64),
		lastParentEmitAt:  make(map[string]int64),
		lastParentMutated: make(map[string]bool),
		lastParentChildID: make(map[string]string),
		childMetaByID:     make(map[string]map[string]any),
		childMutatedByID:  make(map[string]bool),
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
	childID := b.tr.ChildSessionIDForKey(callID)

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
	b.lastParentEmitAt[callID] = nowMs
	if childID != "" {
		b.lastParentChildID[callID] = childID
	}
	if b.childMutatedByID[callID] {
		b.lastParentMutated[callID] = true
	}
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

	toolMeta := cloneGenericMap(evt.Metadata)
	if evt.Kind == NativeEventToolCompleted || evt.Kind == NativeEventToolError {
		if toolMeta == nil {
			toolMeta = make(map[string]interface{})
		}
		if evt.Step > 0 {
			toolMeta["step"] = evt.Step
		}
		// Saving attachments performs disk I/O, so do it outside translator locks.
		if b.svc != nil && len(evt.Media) > 0 {
			childID := b.tr.ChildSessionIDForKey(parentCallID)
			if rec, ok := b.svc.store.Get(childID); ok {
				if refs := b.svc.saveNativeToolMediaAttachments(rec, evt.Media); len(refs) > 0 {
					toolMeta["mediaAttachments"] = refs
				}
			}
		}
	}
	var evts []SSEEvent
	var childID string
	switch evt.Kind {
	case NativeEventSubtaskStart:
		childProv := metaStr(evt.Metadata, "providerID")
		childModel := firstNonEmptyStr(metaStr(evt.Metadata, "modelID"), b.modelID)
		childMode := firstNonEmptyStr(metaStr(evt.Metadata, "mode"), "plan")
		subType := metaStr(evt.Metadata, "subagent_type")
		evts, childID = b.tr.OpenChildSession(parentCallID, ChildSessionSpec{
			Title:      firstNonEmptyStr(evt.Title, "Subagent"),
			Prompt:     evt.Text,
			ProviderID: childProv,
			ModelID:    childModel,
			Mode:       childMode,
			Agent:      subType,
		}, nowMs)

	case NativeEventThinkingEnd:
		suffix := firstNonEmptyStr(evt.PartID, fmt.Sprintf("think_%d", nowMs))
		evts, childID = b.tr.UpsertChildText(parentCallID, "reasoning", suffix, evt.Text, nowMs, nowMs)

	case NativeEventTextEnd:
		suffix := firstNonEmptyStr(evt.PartID, fmt.Sprintf("text_%d", nowMs))
		evts, childID = b.tr.UpsertChildText(parentCallID, "text", suffix, evt.Text, nowMs, nowMs)

	case NativeEventToolRunning:
		key := firstNonEmptyStr(evt.CallID, evt.PartID, fmt.Sprintf("tool_%d", nowMs))
		runMeta := cloneGenericMap(evt.Metadata)
		evts, childID = b.tr.StartChildTool(parentCallID, key, evt.CallID, evt.ToolName, evt.Input, runMeta, nowMs)

	case NativeEventToolCompleted, NativeEventToolError:
		isError := evt.Kind == NativeEventToolError
		evts, childID = b.tr.FinishChildTool(parentCallID, evt.CallID, firstNonEmptyStr(evt.ErrorText, evt.Text), isError, evt.Step, toolMeta, nowMs)

	case NativeEventSubtaskEnd:
		evts, childID = b.tr.CloseChildSession(parentCallID, strings.TrimSpace(evt.ErrorText) != "", nowMs)

	default:
		childID = b.tr.ChildSessionIDForKey(parentCallID)
	}

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
		isLifecycle := evt.Kind == NativeEventSubtaskStart || evt.Kind == NativeEventSubtaskEnd
		isToolEvt := evt.Kind == NativeEventToolRunning || evt.Kind == NativeEventToolCompleted || evt.Kind == NativeEventToolError
		stateChanged := (childID != "" && childID != b.lastParentChildID[parentCallID]) ||
			(b.childMutatedByID[parentCallID] && !b.lastParentMutated[parentCallID])
		heartbeatDue := isToolEvt && (b.lastParentEmitAt[parentCallID] == 0 || nowMs-b.lastParentEmitAt[parentCallID] >= nativeSubagentHeartbeatIntervalMs)
		if isLifecycle || stateChanged || heartbeatDue {
			b.lastParentEmitAt[parentCallID] = nowMs
			if childID != "" {
				b.lastParentChildID[parentCallID] = childID
			}
			if b.childMutatedByID[parentCallID] {
				b.lastParentMutated[parentCallID] = true
			}
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
