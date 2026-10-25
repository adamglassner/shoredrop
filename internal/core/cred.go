package core

import "errors"

// CredStore stores secrets in the operating system's secure store.
type CredStore interface {
	Set(target, user, secret string) error
	Get(target string) (string, error)
	Delete(target string) error
}

// ErrNoCredential means nothing is stored under the target.
var ErrNoCredential = errors.New("no saved password")
