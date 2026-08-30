// Package seen tracks post ids already harvested, so a post is only ever sent
// to Claude once. Plain JSON file — small, greppable, easy to hand-edit.
package seen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	path string
	ids  map[string]int64 // post id -> first-seen unix seconds
}

func Load(path string) (*Store, error) {
	s := &Store{path: path, ids: map[string]int64{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read seen store %s: %w", path, err)
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.ids); err != nil {
		// A corrupt store must not kill the run; worst case is re-filtering posts.
		return &Store{path: path, ids: map[string]int64{}}, fmt.Errorf("seen store unreadable, starting fresh: %w", err)
	}
	return s, nil
}

func (s *Store) Has(id string) bool { _, ok := s.ids[id]; return ok }
func (s *Store) Len() int           { return len(s.ids) }

func (s *Store) Add(ids ...string) {
	now := time.Now().Unix()
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := s.ids[id]; !ok {
			s.ids[id] = now
		}
	}
}

// Prune drops entries older than days. Returns how many went.
func (s *Store) Prune(days int) int {
	if days <= 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	n := 0
	for id, ts := range s.ids {
		if ts < cutoff {
			delete(s.ids, id)
			n++
		}
	}
	return n
}

// Save writes atomically so an interrupted run can't truncate the store.
func (s *Store) Save() error {
	b, err := json.Marshal(s.ids)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write seen store: %w", err)
	}
	return os.Rename(tmp, s.path)
}
