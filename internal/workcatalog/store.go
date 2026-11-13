package workcatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const maxStateBytes = 128 << 20

type state struct {
	Version       int                `json:"version"`
	StoreID       string             `json:"storeId"`
	SellerLoginID string             `json:"sellerLoginId"`
	Products      map[string]Product `json:"products"`
	Drafts        map[string]Draft   `json:"drafts"`
	Sync          *syncRun           `json:"sync,omitempty"`
	LastSync      *SyncResult        `json:"lastSync,omitempty"`
}

// Store uses OS file locks across desktop/dev processes, and atomic replacement
// within each store. No global mutex is held during disk or network I/O.
type Store struct{ root string }

func NewStore(root string) *Store {
	if root == "" {
		root = hoxipaths.Join("work-catalog")
	}
	return &Store{root: root}
}

func (s *Store) path(scope Scope) (string, error) {
	if s.root == "" || scope.StoreID == "" || scope.SellerLoginID == "" {
		return "", errors.New("store ID, seller login ID and state directory are required")
	}
	sum := sha256.Sum256([]byte(scope.StoreID))
	return filepath.Join(s.root, hex.EncodeToString(sum[:])+".json"), nil
}

func (s *Store) lock(ctx context.Context, scope Scope, suffix string) (func(), error) {
	path, err := s.path(scope)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return nil, err
	}
	lock := flock.New(path + suffix)
	lockCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(lockCtx, 25*time.Millisecond)
	if err != nil || !ok {
		_ = lock.Close()
		return nil, errors.New("store is busy; retry after the current operation")
	}
	return func() { _ = lock.Close() }, nil
}

func (s *Store) read(scope Scope) (*state, error) {
	path, err := s.path(scope)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return &state{Version: 1, StoreID: scope.StoreID, SellerLoginID: scope.SellerLoginID,
			Products: map[string]Product{}, Drafts: map[string]Draft{}}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxStateBytes {
		return nil, errors.New("catalog state exceeds size limit")
	}
	var st state
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	decodeErr := dec.Decode(&st)
	if decodeErr == nil {
		var extra any
		if dec.Decode(&extra) != io.EOF {
			decodeErr = errors.New("trailing state data")
		}
	}
	if decodeErr != nil || st.Version != 1 || st.Products == nil || st.Drafts == nil {
		checksum := sha256.Sum256(raw)
		backup := path + ".corrupt-" + hex.EncodeToString(checksum[:])
		if err := hoxipaths.WriteFileAtomic(backup, raw, 0600); err != nil {
			return nil, fmt.Errorf("cannot back up corrupt catalog: %w", err)
		}
		return nil, errors.New("catalog is corrupt; a backup was saved and the original was preserved")
	}
	if st.StoreID != scope.StoreID || st.SellerLoginID != scope.SellerLoginID {
		return nil, errors.New("catalog owner does not match the bound store; use a new store ID for a different seller")
	}
	return &st, nil
}

func (s *Store) load(ctx context.Context, scope Scope) (*state, error) {
	unlock, err := s.lock(ctx, scope, ".lock")
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.read(scope)
}

func (s *Store) update(ctx context.Context, scope Scope, fn func(*state) error) error {
	unlock, err := s.lock(ctx, scope, ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.read(scope)
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if len(raw) > maxStateBytes {
		return errors.New("catalog state exceeds size limit")
	}
	path, err := s.path(scope)
	if err != nil {
		return err
	}
	return hoxipaths.WriteFileAtomic(path, raw, 0600)
}
