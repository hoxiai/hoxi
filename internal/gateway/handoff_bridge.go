package gateway

import (
	"context"

	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/runner"
)

func wireHandoffNativeMaterializer(handoffSvc *handoff.Service, runnerSvc *runner.Service) {
	if handoffSvc == nil || runnerSvc == nil {
		return
	}
	handoffSvc.SetNativeMaterializer(func(_ context.Context, params handoff.NativeMaterializeParams) (map[string]interface{}, error) {
		turns := make([]runner.NativeHandoffTurn, 0, len(params.Turns))
		for _, mt := range params.Turns {
			var tok *runner.NativeHandoffTokens
			if mt.Tokens != nil {
				tok = &runner.NativeHandoffTokens{
					Total:      mt.Tokens.Total,
					Context:    mt.Tokens.Context,
					Input:      mt.Tokens.Input,
					Output:     mt.Tokens.Output,
					Reasoning:  mt.Tokens.Reasoning,
					CacheRead:  mt.Tokens.CacheRead,
					CacheWrite: mt.Tokens.CacheWrite,
				}
			}
			parts := make([]runner.NativeHandoffPart, 0, len(mt.Parts))
			for _, mp := range mt.Parts {
				parts = append(parts, runner.NativeHandoffPart{
					ID:       mp.ID,
					Type:     mp.Type,
					Text:     mp.Text,
					Tool:     mp.Tool,
					CallID:   mp.CallID,
					Title:    mp.Title,
					Status:   mp.Status,
					Input:    mp.Input,
					Output:   mp.Output,
					StartMs:  mp.StartMs,
					EndMs:    mp.EndMs,
					Metadata: mp.Metadata,
				})
			}
			turns = append(turns, runner.NativeHandoffTurn{
				ID:          mt.ID,
				ParentID:    mt.ParentID,
				Role:        mt.Role,
				CreatedMs:   mt.CreatedMs,
				CompletedMs: mt.CompletedMs,
				ProviderID:  mt.ProviderID,
				ModelID:     mt.ModelID,
				Mode:        mt.Mode,
				Cost:        mt.Cost,
				Finish:      mt.Finish,
				Tokens:      tok,
				Parts:       parts,
			})
		}
		return runnerSvc.MaterializeNativeHandoff(runner.NativeHandoffRequest{
			TargetDirectory: params.TargetDirectory,
			Title:           params.Title,
			ProviderID:      params.ProviderID,
			ModelID:         params.ModelID,
			CreatedAt:       params.CreatedAt,
			UpdatedAt:       params.UpdatedAt,
			Turns:           turns,
		})
	})
}
