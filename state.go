package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// SeenState persists lightweight global state — currently just the cookie hash,
// used to detect when cookies changed while the bot was down. Per-keyword dedup
// cursors live in keywords.json (KeywordStore), not here.
type SeenState struct {
	CookieHash string    `json:"cookie_hash"`
	UpdatedAt  time.Time `json:"updated_at"`
	mu         sync.RWMutex
	path       string
}

func NewSeenState(path string) *SeenState {
	s := &SeenState{path: path}
	s.load()
	return s
}

func (s *SeenState) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, s)
}

func (s *SeenState) Save() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		logError("[state] marshal: %v", err)
		return
	}
	if err := os.WriteFile(s.path, data, 0644); err != nil {
		logError("[state] save: %v", err)
	}
}

func (s *SeenState) SetCookieHash(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CookieHash = hash
}

func (s *SeenState) GetCookieHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CookieHash
}
