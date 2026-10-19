package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/runner"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiItalic = "\033[3m"
	ansiCyan   = "\033[36m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
)

// TurnRenderer formats unified Hoxi SSE events into a clean terminal stream.
type TurnRenderer struct {
	mu           sync.Mutex
	out          io.Writer
	errOut       io.Writer
	in           io.Reader
	client       *Client
	directory    string
	msgRoles     map[string]string
	partTypes    map[string]string
	printedBytes map[string]int
	toolStates   map[string]string
	inReasoning  bool
	lineOpen     bool
}

func NewTurnRenderer(in io.Reader, out, errOut io.Writer, client *Client, directory string) *TurnRenderer {
	return &TurnRenderer{
		out:          out,
		errOut:       errOut,
		in:           in,
		client:       client,
		directory:    directory,
		msgRoles:     make(map[string]string),
		partTypes:    make(map[string]string),
		printedBytes: make(map[string]int),
		toolStates:   make(map[string]string),
	}
}

func (r *TurnRenderer) HandleEvent(ctx context.Context, evt runner.SSEEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch evt.Type {
	case "message.updated":
		raw, _ := json.Marshal(evt.Properties["info"])
		var info runner.MessageInfo
		if json.Unmarshal(raw, &info) == nil && info.ID != "" {
			r.msgRoles[info.ID] = info.Role
		}

	case "message.part.updated":
		raw, _ := json.Marshal(evt.Properties["part"])
		var part runner.MessagePart
		if json.Unmarshal(raw, &part) != nil || part.ID == "" {
			return
		}
		if r.isUserMessage(part.MessageID, part.ID) {
			return
		}
		r.partTypes[part.ID] = part.Type
		switch part.Type {
		case "reasoning":
			r.writeIncrementalText(part.ID, part.Text, true)
		case "text":
			r.writeIncrementalText(part.ID, part.Text, false)
		case "tool":
			r.renderToolPart(part)
		}

	case "message.part.delta":
		msgID, _ := evt.Properties["messageID"].(string)
		partID, _ := evt.Properties["partID"].(string)
		delta, _ := evt.Properties["delta"].(string)
		field, _ := evt.Properties["field"].(string)
		if partID == "" || delta == "" || r.isUserMessage(msgID, partID) {
			return
		}
		if field != "" && field != "text" {
			return
		}
		pType := r.partTypes[partID]
		isReasoning := pType == "reasoning" || strings.Contains(partID, "_r_")
		r.appendDelta(partID, delta, isReasoning)

	case "permission.asked":
		r.closeOpenLine()
		r.promptPermission(ctx, evt.Properties)
	}
}

func (r *TurnRenderer) Finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeOpenLine()
}

func (r *TurnRenderer) isUserMessage(msgID, partID string) bool {
	if r.msgRoles[msgID] == "user" {
		return true
	}
	return strings.HasPrefix(msgID, "msg_u_") || strings.HasPrefix(partID, "prt_u_")
}

func (r *TurnRenderer) appendDelta(partID, delta string, isReasoning bool) {
	if isReasoning && !r.inReasoning {
		r.closeOpenLine()
		fmt.Fprintf(r.out, "%s%s💭 ", ansiDim, ansiItalic)
		r.inReasoning = true
	} else if !isReasoning && r.inReasoning {
		fmt.Fprintf(r.out, "%s\n\n", ansiReset)
		r.inReasoning = false
		r.lineOpen = false
	}
	fmt.Fprint(r.out, delta)
	r.printedBytes[partID] += len(delta)
	r.lineOpen = !strings.HasSuffix(delta, "\n")
}

func (r *TurnRenderer) writeIncrementalText(partID, fullText string, isReasoning bool) {
	already := r.printedBytes[partID]
	if len(fullText) <= already {
		return
	}
	suffix := fullText[already:]
	r.appendDelta(partID, suffix, isReasoning)
	r.printedBytes[partID] = len(fullText)
}

func (r *TurnRenderer) renderToolPart(part runner.MessagePart) {
	if part.State == nil {
		return
	}
	status := part.State.Status
	prev := r.toolStates[part.ID]
	if status == prev || status == "pending" {
		return
	}
	r.toolStates[part.ID] = status
	r.closeOpenLine()

	toolName := firstNonEmpty(part.Tool, "tool")
	summary := strings.TrimSpace(part.State.Title)
	if summary == "" && part.State.Input != nil {
		for _, k := range []string{"command", "CommandLine", "filePath", "path", "AbsolutePath", "pattern", "query"} {
			if v, ok := part.State.Input[k].(string); ok && strings.TrimSpace(v) != "" {
				summary = strings.TrimSpace(v)
				break
			}
		}
	}
	if len(summary) > 76 {
		summary = summary[:73] + "..."
	}

	switch status {
	case "running":
		if summary != "" {
			fmt.Fprintf(r.out, "%s  ⟳ %s%s %s%s%s\n", ansiCyan, toolName, ansiReset, ansiDim, summary, ansiReset)
		} else {
			fmt.Fprintf(r.out, "%s  ⟳ %s%s\n", ansiCyan, toolName, ansiReset)
		}
	case "completed":
		dur := ""
		if part.State.Time != nil && part.State.Time.Start > 0 && part.State.Time.End >= part.State.Time.Start {
			dur = fmt.Sprintf(" (%dms)", part.State.Time.End-part.State.Time.Start)
		}
		if summary != "" {
			fmt.Fprintf(r.out, "%s  ✓ %s%s %s%s%s%s\n", ansiGreen, toolName, ansiReset, ansiDim, summary, dur, ansiReset)
		} else {
			fmt.Fprintf(r.out, "%s  ✓ %s%s%s\n", ansiGreen, toolName, dur, ansiReset)
		}
	case "error":
		errMsg := firstNonEmpty(part.State.Error, "failed")
		fmt.Fprintf(r.out, "%s  ✗ %s: %s%s\n", ansiRed, toolName, errMsg, ansiReset)
	}
}

func (r *TurnRenderer) promptPermission(ctx context.Context, props map[string]interface{}) {
	reqID, _ := props["id"].(string)
	if reqID == "" || r.client == nil {
		return
	}
	perm, _ := props["permission"].(string)
	cmdText := ""
	if meta, ok := props["metadata"].(map[string]interface{}); ok {
		if c, ok := meta["command"].(string); ok {
			cmdText = c
		}
	}
	if cmdText == "" {
		if patterns, ok := props["patterns"].([]interface{}); ok && len(patterns) > 0 {
			cmdText, _ = patterns[0].(string)
		}
	}
	fmt.Fprintf(r.out, "\n%s%s⚠️  Approval Required [%s]%s\n", ansiYellow, ansiBold, firstNonEmpty(perm, "tool"), ansiReset)
	if cmdText != "" {
		fmt.Fprintf(r.out, "   Command: %s%s%s\n", ansiBold, cmdText, ansiReset)
	}
	fmt.Fprintf(r.out, "   Allow? %s[y]es once / [a]lways / [n]o (or approve in Hoxi Desktop/Mobile): %s", ansiDim, ansiReset)

	if r.in == nil {
		return
	}
	reader, ok := r.in.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(r.in)
	}
	line, _ := reader.ReadString('\n')
	choice := strings.ToLower(strings.TrimSpace(line))
	reply := "reject"
	switch choice {
	case "y", "yes", "":
		reply = "once"
	case "a", "always":
		reply = "always"
	}
	_ = r.client.ReplyPermission(ctx, reqID, r.directory, reply)
}

func (r *TurnRenderer) closeOpenLine() {
	if r.inReasoning {
		fmt.Fprint(r.out, ansiReset)
		r.inReasoning = false
	}
	if r.lineOpen {
		fmt.Fprintln(r.out)
		r.lineOpen = false
	}
}
