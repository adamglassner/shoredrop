//go:build !windows

package core

import (
	"bytes"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// On macOS secrets go to the login Keychain via the security tool. On other
// systems (used only for development and tests) they are kept in memory.

type keychainStore struct{}

func (keychainStore) Set(target, user, secret string) error {
	return exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", target, "-a", user, "-w", secret).Run()
}

func (keychainStore) Get(target string) (string, error) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", target, "-w").Output()
	if err != nil {
		return "", ErrNoCredential
	}
	return strings.TrimRight(string(bytes.TrimRight(out, "\n")), "\r"), nil
}

func (keychainStore) Delete(target string) error {
	exec.Command("/usr/bin/security", "delete-generic-password", "-s", target).Run()
	return nil
}

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
var Credentials CredStore = func() CredStore {
	if runtime.GOOS == "darwin" {
		return keychainStore{}
	}
	return &memStore{m: map[string]string{}}
}()

// CredentialStoreName is shown in the UI.
var CredentialStoreName = func() string {
	if runtime.GOOS == "darwin" {
		return "macOS Keychain"
	}
	return "in-memory store"
}()
