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

func sqliteImmutableURI(dbPath string) string {
	slash := filepath.ToSlash(dbPath)
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(slash)
	if len(escaped) >= 2 && escaped[1] == ':' {
		return "file:///" + escaped + "?immutable=1"
	}
	return "file:" + escaped + "?immutable=1"
}

// countAgyStoreSteps is a variable so tests can simulate a missing sqlite3.
var countAgyStoreSteps = func(dbPath string) (int, error) {
	query := func(target string) (int, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := subproc.CommandContext(ctx, sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 2000", target, "SELECT count(*) FROM steps;").Output()
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(string(out)))
	}
	n, err := query(dbPath)
	if err != nil {
		// agy unlinks <id>.db-wal and <id>.db-shm on exit while leaving the
		// database header in WAL mode (02 02). sqlite3 -readonly fails with
		// SQLITE_CANTOPEN (14) when -shm is absent because a read-only handle
		// cannot create the shared-memory index. When no -wal file exists on
		// disk, there are no uncheckpointed frames, so immutable=1 safely
		// inspects the checkpointed store without creating sidecars.
		if _, walErr := os.Lstat(dbPath + "-wal"); os.IsNotExist(walErr) {
			return query(sqliteImmutableURI(dbPath))
		}
		return 0, err
	}
	return n, nil
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
