//go:build windows

package core

import (
	"errors"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Windows Credential Manager (generic credentials), via advapi32.

var (
	advapi32       = syscall.NewLazyDLL("advapi32.dll")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredDelete = advapi32.NewProc("CredDeleteW")
	procCredFree   = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric      = 1
	credPersistLocalMach = 2
	errNotFound          = 1168 // ERROR_NOT_FOUND
)

type winCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        [2]uint32
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type winCredStore struct{}

// Credentials is the operating system's secure credential store.
var Credentials CredStore = winCredStore{}

// CredentialStoreName is shown in the UI.
const CredentialStoreName = "Windows Credential Manager"

func (winCredStore) Set(target, user, secret string) error {
	t, _ := syscall.UTF16PtrFromString(target)
	u, _ := syscall.UTF16PtrFromString(user)
	c, _ := syscall.UTF16PtrFromString("Saved by ShoreDrop")
	blob := utf16.Encode([]rune(secret))
	var bp *byte
	if len(blob) > 0 {
		bp = (*byte)(unsafe.Pointer(&blob[0]))
	}
	cred := winCredential{
		Type:               credTypeGeneric,
		TargetName:         t,
		Comment:            c,
		CredentialBlobSize: uint32(len(blob) * 2),
		CredentialBlob:     bp,
		Persist:            credPersistLocalMach,
		UserName:           u,
	}
	r, _, err := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r == 0 {
		return err
	}
	return nil
}

func (winCredStore) Get(target string) (string, error) {
	t, _ := syscall.UTF16PtrFromString(target)
	var p *winCredential
	r, _, err := procCredReadW.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&p)))
	if r == 0 {
		if en, ok := err.(syscall.Errno); ok && en == errNotFound {
			return "", ErrNoCredential
		}
		return "", err
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(p)))
	n := int(p.CredentialBlobSize) / 2
	if n == 0 || p.CredentialBlob == nil {
		return "", nil
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(p.CredentialBlob)), n)
	return string(utf16.Decode(u)), nil
}

func (winCredStore) Delete(target string) error {
	t, _ := syscall.UTF16PtrFromString(target)
	r, _, err := procCredDelete.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0)
	if r == 0 {
		if en, ok := err.(syscall.Errno); ok && en == errNotFound {
			return nil
		}
		return err
	}
	return nil
}

var _ = errors.New
