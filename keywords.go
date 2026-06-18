package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Keyword is a single search-monitor entry: an X search query bound to a Discord
// channel, with its own dedup cursor and optional filters.
type Keyword struct {
	ID         string `json:"id"`           // short slug, unique (e.g. "freemint")
	Query      string `json:"query"`        // raw X search query (operators allowed)
	ChannelID  string `json:"channel_id"`   // Discord channel to push matches to
	LastSeenID string `json:"last_seen_id"` // newest tweet ID already pushed (dedup cursor)
	Enabled    bool   `json:"enabled"`
	MinFaves   int    `json:"min_faves,omitempty"`   // post-filter: skip tweets below this like count
	MinFollow  int    `json:"min_follow,omitempty"`  // reserved (author follower filter, future)
	AddedAt    string `json:"added_at,omitempty"`
}

// KeywordStore is the persistent set of keyword monitors, keyed by ID.
type KeywordStore struct {
	mu       sync.RWMutex
	path     string
	Keywords []*Keyword `json:"keywords"`
}

// NewKeywordStore loads (or creates) the keyword store from data/keywords.json.
func NewKeywordStore(dataDir string) (*KeywordStore, error) {
	ks := &KeywordStore{
		path:     filepath.Join(dataDir, "keywords.json"),
		Keywords: []*Keyword{},
	}
	if err := ks.load(); err != nil {
		return nil, err
	}
	return ks, nil
}

func (ks *KeywordStore) load() error {
	data, err := os.ReadFile(ks.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh store
		}
		return fmt.Errorf("read keywords: %w", err)
	}
	var wrap struct {
		Keywords []*Keyword `json:"keywords"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return fmt.Errorf("parse keywords: %w", err)
	}
	ks.Keywords = wrap.Keywords
	if ks.Keywords == nil {
		ks.Keywords = []*Keyword{}
	}
	return nil
}

// Save persists the store atomically.
func (ks *KeywordStore) Save() error {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.saveLocked()
}

func (ks *KeywordStore) saveLocked() error {
	wrap := struct {
		Keywords []*Keyword `json:"keywords"`
	}{Keywords: ks.Keywords}
	data, err := json.MarshalIndent(wrap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal keywords: %w", err)
	}
	tmp := ks.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write keywords: %w", err)
	}
	return os.Rename(tmp, ks.path)
}

// Count returns the number of keyword entries.
func (ks *KeywordStore) Count() int {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return len(ks.Keywords)
}

// List returns a snapshot copy of all keywords.
func (ks *KeywordStore) List() []Keyword {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	out := make([]Keyword, len(ks.Keywords))
	for i, k := range ks.Keywords {
		out[i] = *k
	}
	return out
}

// EnabledList returns a snapshot copy of enabled keywords only.
func (ks *KeywordStore) EnabledList() []Keyword {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	var out []Keyword
	for _, k := range ks.Keywords {
		if k.Enabled {
			out = append(out, *k)
		}
	}
	return out
}

// slugify turns an arbitrary id/query into a safe lowercase slug.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Add inserts a new keyword. If id is empty it is derived from the query.
// Returns an error if the id already exists.
func (ks *KeywordStore) Add(id, query, channelID string, minFaves int) (*Keyword, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	if id == "" {
		id = slugify(query)
	}
	if id == "" {
		return nil, fmt.Errorf("could not derive an id from query %q — pass an explicit id", query)
	}
	for _, k := range ks.Keywords {
		if k.ID == id {
			return nil, fmt.Errorf("keyword id %q already exists", id)
		}
	}
	kw := &Keyword{
		ID:        id,
		Query:     query,
		ChannelID: channelID,
		Enabled:   true,
		MinFaves:  minFaves,
		AddedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	ks.Keywords = append(ks.Keywords, kw)
	if err := ks.saveLocked(); err != nil {
		return nil, err
	}
	cp := *kw
	return &cp, nil
}

// Remove deletes a keyword by id. Returns false if not found.
func (ks *KeywordStore) Remove(id string) (bool, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for i, k := range ks.Keywords {
		if k.ID == id {
			ks.Keywords = append(ks.Keywords[:i], ks.Keywords[i+1:]...)
			return true, ks.saveLocked()
		}
	}
	return false, nil
}

// SetEnabled toggles a keyword. Returns false if not found.
func (ks *KeywordStore) SetEnabled(id string, enabled bool) (bool, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for _, k := range ks.Keywords {
		if k.ID == id {
			k.Enabled = enabled
			return true, ks.saveLocked()
		}
	}
	return false, nil
}

// Get returns a copy of a keyword by id.
func (ks *KeywordStore) Get(id string) (Keyword, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	for _, k := range ks.Keywords {
		if k.ID == id {
			return *k, true
		}
	}
	return Keyword{}, false
}

// SetLastSeen updates the dedup cursor for a keyword and persists.
func (ks *KeywordStore) SetLastSeen(id, tweetID string) {
	ks.mu.Lock()
	changed := false
	for _, k := range ks.Keywords {
		if k.ID == id {
			if tweetID > k.LastSeenID {
				k.LastSeenID = tweetID
				changed = true
			}
			break
		}
	}
	if changed {
		_ = ks.saveLocked()
	}
	ks.mu.Unlock()
}
