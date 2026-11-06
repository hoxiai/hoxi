package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/sqlitecli"
	"github.com/hoxiai/hoxi/internal/subproc"
)

// agy sometimes writes a new conversation to the Protobuf store (<id>.pb) and
// also leaves an empty SQLite store (<id>.db). Resuming then opens the empty
// SQLite store and the conversation loses its history. When the SQLite store
// provably holds no steps, moving it aside lets agy resume from <id>.pb with
// the full history and a warm prompt cache, instead of a fresh conversation
// that replays a lossy text summary.

const agyEmptyStoreSuffix = ".hoxi-empty-"

// countAgyStoreSteps is a variable so tests can simulate a missing sqlite3.
var countAgyStoreSteps = func(dbPath string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := subproc.CommandContext(ctx, sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 2000", dbPath, "SELECT count(*) FROM steps;").Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func nonEmptyRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// quarantineEmptyAgySQLiteStore renames the empty SQLite store (and its WAL
// and shared-memory files) of a split conversation. It reports true only when
// the conversation is no longer split afterwards; any store that cannot be
// proven empty is left untouched so the caller falls back to a fresh
// conversation.
func quarantineEmptyAgySQLiteStore(conversationID string) bool {
	conversationID = strings.TrimSpace(conversationID)
	if !validStoreID(conversationID) {
		return false
	}
	for _, root := range agyConversationRoots() {
		dbPath := filepath.Join(root, conversationID+".db")
		if !nonEmptyRegularFile(filepath.Join(root, conversationID+".pb")) || !nonEmptyRegularFile(dbPath) {
			continue
		}
		if steps, err := countAgyStoreSteps(dbPath); err != nil || steps != 0 {
			return false
		}
		suffix := agyEmptyStoreSuffix + strconv.FormatInt(time.Now().UnixMilli(), 10)
		// Sidecars move first: a leftover -wal next to a future <id>.db would be
		// replayed into it.
		var moved []string
		for _, path := range []string{dbPath + "-wal", dbPath + "-shm", dbPath} {
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				continue
			}
			if err := os.Rename(path, path+suffix); err != nil {
				for _, done := range moved {
					_ = os.Rename(done+suffix, done)
				}
				return false
			}
			moved = append(moved, path)
		}
	}
	return !hasAgySplitBrainConversation(conversationID)
}
