//go:build windows

package ui

import (
	"syscall"
	"unsafe"
)

// Minimal COM bindings for the modern Windows file/folder picker (IFileOpenDialog).

type guid struct {
	D1 uint32
	D2 uint16
	D3 uint16
	D4 [8]byte
}

var (
	clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidIFileOpenDialog  = guid{0xd57c7288, 0xd4ad, 0x4768, [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
	iidIShellItem       = guid{0x43826d1e, 0xe718, 0x42ee, [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}

	pCoCreateInstance        = ole32.NewProc("CoCreateInstance")
	pSHCreateItemFromParsing = shell32.NewProc("SHCreateItemFromParsingName")
)

func comCall(obj uintptr, index int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(index)*unsafe.Sizeof(uintptr(0))))
	all := append([]uintptr{obj}, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2)
	}
}

func showFileDialog(owner *Window, title, initial string, folders bool) (string, bool) {
	var dlg uintptr
	hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, 1, /*CLSCTX_INPROC_SERVER*/
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)), uintptr(unsafe.Pointer(&dlg)))
	if hr != 0 || dlg == 0 {
		return "", false
	}
	defer comRelease(dlg)
	var opts uint32
	comCall(dlg, 10, uintptr(unsafe.Pointer(&opts))) // GetOptions
	opts |= 0x40                                     // FOS_FORCEFILESYSTEM
	if folders {
		opts |= 0x20 // FOS_PICKFOLDERS
	} else {
		opts |= 0x1000 // FOS_FILEMUSTEXIST
	}
	comCall(dlg, 9, uintptr(opts)) // SetOptions
	comCall(dlg, 17, up(title))    // SetTitle
	if initial != "" {
		var item uintptr
		if r, _, _ := pSHCreateItemFromParsing.Call(up(initial), 0, uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&item))); r == 0 && item != 0 {
			comCall(dlg, 12, item) // SetFolder
			comRelease(item)
		}
	}
	var hwnd uintptr
	if owner != nil {
		hwnd = owner.hwnd
	}
	if comCall(dlg, 3, hwnd) != 0 { // Show (cancelled or failed)
		return "", false
	}
	var item uintptr
	if comCall(dlg, 20, uintptr(unsafe.Pointer(&item))) != 0 || item == 0 { // GetResult
		return "", false
	}
	defer comRelease(item)
	var name *uint16
	if comCall(item, 5, 0x80058000 /*SIGDN_FILESYSPATH*/, uintptr(unsafe.Pointer(&name))) != 0 || name == nil {
		return "", false
	}
	defer pCoTaskMemFree.Call(uintptr(unsafe.Pointer(name)))
	s := syscall.UTF16ToString(unsafe.Slice(name, 32768))
	return s, s != ""
}

func pickFolder(owner *Window, title, initial string) (string, bool) {
	return showFileDialog(owner, title, initial, true)
}

func pickFile(owner *Window, title string) (string, bool) {
	return showFileDialog(owner, title, "", false)
}
