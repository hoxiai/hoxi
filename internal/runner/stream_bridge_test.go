package runner

import "github.com/hoxiai/hoxi/internal/runner/turnstream"

const agyEmptyStoreSuffix = turnstream.AgyEmptyStoreSuffix

func nonEmptyRegularFile(path string) bool { return turnstream.NonEmptyRegularFile(path) }

func setCountAgyStoreStepsForTest(fn func(string) (int, error)) func() {
	return turnstream.SetCountAgyStoreStepsForTest(fn)
}

func newAgyEditPayloadCursor(conversationID string) *turnstream.AgyEditPayloadCursor {
	return turnstream.NewAgyEditPayloadCursor(conversationID)
}
