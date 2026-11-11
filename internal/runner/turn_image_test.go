package runner

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

type fakeImageClient struct {
	mu       sync.Mutex
	requests []llm.ImageRequest
	pngBytes []byte
}

func (f *fakeImageClient) IsImageModel(_, modelID string) bool {
	return strings.Contains(strings.ToLower(modelID), "-image") || strings.HasPrefix(strings.ToLower(modelID), "imagen")
}

func (f *fakeImageClient) ListImageModels() []llm.ImageModelOption {
	return []llm.ImageModelOption{{ProviderID: "custom-gemini", ModelID: "gemini-2.5-flash-image", SupportsIn: true}}
}

func (f *fakeImageClient) ResolveImageModel(preferredProviderID, preferredModelID string) (string, string, bool) {
	if preferredProviderID != "" && preferredModelID != "" {
		return preferredProviderID, preferredModelID, true
	}
	return "custom-gemini", "gemini-2.5-flash-image", true
}

func (f *fakeImageClient) GenerateImage(_ context.Context, req llm.ImageRequest) (llm.ImageResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	return llm.ImageResponse{
		Text: "已为你生成赛博朋克猫咪插画",
		Images: []llm.GeneratedImage{
			{
				Mime: "image/png",
				Data: f.pngBytes,
			},
		},
		InputTokens:  24,
		OutputTokens: 1290,
	}, nil
}

func TestSessionImageGenerationTurnPersistsAttachmentAndStreamsFilePart(t *testing.T) {
	pngBytes, _ := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
	)
	projectDir := t.TempDir()
	storeDir := t.TempDir()

	svc := NewService(projectDir, nil, nil, storeDir)
	defer svc.CloseAll()

	fakeImg := &fakeImageClient{pngBytes: pngBytes}
	svc.SetImageClient(fakeImg)

	info, status, err := svc.CreateConversation(context.Background(), CreateSessionRequest{
		Directory:   projectDir,
		SandboxMode: "local",
		ProviderID:  "custom-gemini",
		ModelID:     "gemini-2.5-flash-image",
	}, projectDir)
	if err != nil || status != 200 {
		t.Fatalf("CreateConversation failed: status=%d err=%v", status, err)
	}

	evtCh, unsub := svc.Subscribe(info.ID)
	defer unsub()

	pStatus, receipt, err := svc.startPromptWithReceipt(context.Background(), info.ID, projectDir, PromptRequest{
		Text: "画一只赛博朋克风格的橘猫",
		Model: &PromptModel{
			ProviderID: "custom-gemini",
			ModelID:    "gemini-2.5-flash-image",
		},
	})
	if err != nil || pStatus != 200 || !receipt.OK {
		t.Fatalf("startPromptWithReceipt failed: status=%d receipt=%+v err=%v", pStatus, receipt, err)
	}

	deadline := time.After(5 * time.Second)
	sawIdle := false
	for !sawIdle {
		select {
		case evt := <-evtCh:
			if evt.Type == "session.status" {
				if st, ok := evt.Properties["status"].(SessionStatus); ok && st.Type == "idle" {
					sawIdle = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for image generation turn to reach idle")
		}
	}

	rec, ok := svc.Store().Get(info.ID)
	if !ok {
		t.Fatalf("session %s missing from store", info.ID)
	}
	if len(rec.Turns) != 2 {
		t.Fatalf("expected 2 turns (user + assistant), got %d", len(rec.Turns))
	}
	asstTurn := rec.Turns[1]
	if asstTurn.Info.Role != "assistant" || asstTurn.Info.Finish != "stop" {
		t.Fatalf("unexpected assistant turn info: %+v", asstTurn.Info)
	}

	var sawText, sawImageFile bool
	for _, p := range asstTurn.Parts {
		if p.Type == "text" && strings.Contains(p.Text, "赛博朋克猫咪") {
			sawText = true
		}
		if p.Type == "file" && p.Mime == "image/png" && strings.HasPrefix(p.URL, "/api/hoxi/attachments/") {
			sawImageFile = true
		}
	}
	if !sawText || !sawImageFile {
		t.Fatalf("expected both text and image file parts in assistant turn, got: %+v", asstTurn.Parts)
	}

	// Follow-up turn without explicit attachment should pass the previous turn's generated image for conversational editing.
	_, _, err = svc.startPromptWithReceipt(context.Background(), info.ID, projectDir, PromptRequest{
		Text: "把背景换成霓虹雨夜街道",
		Model: &PromptModel{
			ProviderID: "custom-gemini",
			ModelID:    "gemini-2.5-flash-image",
		},
	})
	if err != nil {
		t.Fatalf("second image turn failed: %v", err)
	}
	sawSecondIdle := false
	deadline2 := time.After(5 * time.Second)
	for !sawSecondIdle {
		select {
		case evt := <-evtCh:
			if evt.Type == "session.status" {
				if st, ok := evt.Properties["status"].(SessionStatus); ok && st.Type == "idle" {
					sawSecondIdle = true
				}
			}
		case <-deadline2:
			t.Fatal("timed out waiting for second image turn")
		}
	}

	fakeImg.mu.Lock()
	defer fakeImg.mu.Unlock()
	if len(fakeImg.requests) != 2 {
		t.Fatalf("expected 2 image requests, got %d", len(fakeImg.requests))
	}
	if len(fakeImg.requests[1].InputImages) != 1 {
		t.Fatalf("expected second turn to attach previous generated image for editing, got %d images", len(fakeImg.requests[1].InputImages))
	}
}
