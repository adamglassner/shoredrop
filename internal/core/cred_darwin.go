//go:build darwin

package core

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <Security/Security.h>

static CFStringRef sdStr(const char *s) {
	return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
}

static CFMutableDictionaryRef sdQuery(const char *service) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef svc = sdStr(service);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, svc);
	CFRelease(svc);
	return q;
}

// sdKeychainSet adds or replaces the password stored under service.
static int sdKeychainSet(const char *service, const char *account, const char *secret, int secretLen) {
	CFMutableDictionaryRef q = sdQuery(service);
	SecItemDelete(q);
	CFStringRef acct = sdStr(account);
	CFStringRef label = sdStr(service);
	CFDataRef data = CFDataCreate(NULL, (const UInt8 *)secret, secretLen);
	CFDictionarySetValue(q, kSecAttrAccount, acct);
	CFDictionarySetValue(q, kSecAttrLabel, label);
	CFDictionarySetValue(q, kSecValueData, data);
	OSStatus st = SecItemAdd(q, NULL);
	CFRelease(acct);
	CFRelease(label);
	CFRelease(data);
	CFRelease(q);
	return (int)st;
}

// sdKeychainGet returns a malloc'd copy of the secret (caller frees) or NULL.
static char *sdKeychainGet(const char *service, int *outLen, int *status) {
	CFMutableDictionaryRef q = sdQuery(service);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef res = NULL;
	OSStatus st = SecItemCopyMatching(q, &res);
	CFRelease(q);
	*status = (int)st;
	if (st != errSecSuccess || res == NULL) {
		return NULL;
	}
	CFDataRef data = (CFDataRef)res;
	CFIndex n = CFDataGetLength(data);
	char *out = malloc(n + 1);
	CFDataGetBytes(data, CFRangeMake(0, n), (UInt8 *)out);
	out[n] = 0;
	*outLen = (int)n;
	CFRelease(res);
	return out;
}

static int sdKeychainDelete(const char *service) {
	CFMutableDictionaryRef q = sdQuery(service);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return (int)st;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// keychainStore keeps secrets in the user's login Keychain as generic
// passwords (service = target, account = user name).
type keychainStore struct{}

func (keychainStore) Set(target, user, secret string) error {
	ct, cu := C.CString(target), C.CString(user)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cu))
	cs := C.CBytes(append([]byte(secret), 0))
	defer func() {
		// wipe our copy of the secret before freeing it
		b := unsafe.Slice((*byte)(cs), len(secret)+1)
		for i := range b {
			b[i] = 0
		}
		C.free(cs)
	}()
	if st := C.sdKeychainSet(ct, cu, (*C.char)(cs), C.int(len(secret))); st != 0 {
		return fmt.Errorf("Keychain error %d", int(st))
	}
	return nil
}

func (keychainStore) Get(target string) (string, error) {
	ct := C.CString(target)
	defer C.free(unsafe.Pointer(ct))
	var n, st C.int
	p := C.sdKeychainGet(ct, &n, &st)
	if p == nil {
		if st == C.errSecItemNotFound {
			return "", ErrNoCredential
		}
		return "", fmt.Errorf("Keychain error %d", int(st))
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoStringN(p, n), nil
}

func (keychainStore) Delete(target string) error {
	ct := C.CString(target)
	defer C.free(unsafe.Pointer(ct))
	C.sdKeychainDelete(ct)
	return nil
}

// Credentials is the platform credential store.
var Credentials CredStore = keychainStore{}

// CredentialStoreName is shown in the UI.
const CredentialStoreName = "the macOS Keychain"
