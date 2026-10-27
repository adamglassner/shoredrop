//go:build !windows && !darwin

package core

import "sync"

// On Linux and other systems (used only for development and tests) secrets
// are kept in memory.

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Set(target, user, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[target] = secret
	return nil
}
func (s *memStore) Get(target string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[target]
	if !ok {
		return "", ErrNoCredential
	}
	return v, nil
}
func (s *memStore) Delete(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, target)
	return nil
}

// Credentials is the platform credential store.
var Credentials CredStore = &memStore{m: map[string]string{}}

// CredentialStoreName is shown in the UI.
const CredentialStoreName = "in-memory store"
