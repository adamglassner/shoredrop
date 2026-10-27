//go:build darwin

package ui

/*
#include <stdlib.h>
#include "mac_darwin.h"
*/
import "C"

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

const (
	drivesName      = "Computer"
	drivesSub       = "Volumes"
	trashName       = "Trash"
	fileManagerName = "Finder"
	osName          = "macOS"
	hiddenFilesHint = "Files starting with a dot, and files hidden in Finder"

	// nativeChrome: the window has the macOS traffic lights instead of
	// custom caption buttons. nativeMenuBar: menus live in the menu bar.
	nativeChrome  = true
	nativeMenuBar = true
)

const ufHidden = 0x8000 // UF_HIDDEN in st_flags

func listLocal(dir string, showHidden bool) ([]FileRow, error) {
	if dir == "" {
		return drives(), nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	rows := []FileRow{{Name: "..", IsParent: true, IsDir: true}}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		hidden := strings.HasPrefix(e.Name(), ".")
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Flags&ufHidden != 0 {
			hidden = true
		}
		if hidden && !showHidden {
			continue
		}
		mode := info.Mode()
		isDir := mode.IsDir()
		isLink := mode&os.ModeSymlink != 0
		if isLink {
			if st, err := os.Stat(filepath.Join(dir, e.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		rows = append(rows, FileRow{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime(), IsDir: isDir, IsLink: isLink,
			Perm: localPerm(mode, isDir), Hidden: hidden})
	}
	return rows, nil
}

// localPerm formats Unix permission bits like ls (drwxr-xr-x).
func localPerm(m os.FileMode, dir bool) string {
	b := []byte("----------")
	if dir {
		b[0] = 'd'
	} else if m&os.ModeSymlink != 0 {
		b[0] = 'l'
	}
	const rwx = "rwxrwxrwx"
	for i := 0; i < 9; i++ {
		if m&(1<<uint(8-i)) != 0 {
			b[i+1] = rwx[i]
		}
	}
	return string(b)
}

// drives lists the startup disk and mounted volumes.
func drives() []FileRow {
	rows := []FileRow{{Name: "/", IsDir: true, IsDrive: true, Perm: "Startup disk"}}
	entries, _ := os.ReadDir("/Volumes")
	var names []string
	for _, e := range entries {
		p := filepath.Join("/Volumes", e.Name())
		if t, err := os.Readlink(p); err == nil && t == "/" {
			continue // the startup disk's alias
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		rows = append(rows, FileRow{Name: filepath.Join("/Volumes", n), IsDir: true, IsDrive: true, Perm: n})
	}
	return rows
}

func parentLocal(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	clean := filepath.Clean(dir)
	if clean == "/" {
		return "", true // → Computer (volumes)
	}
	if filepath.Dir(clean) == "/Volumes" {
		return "", true
	}
	return filepath.Dir(clean), true
}

func normalizeLocal(input, cur string) string {
	t := strings.Trim(strings.TrimSpace(input), `"'`)
	if t == "" || strings.EqualFold(t, drivesName) {
		return ""
	}
	if strings.HasPrefix(t, "~") {
		home, _ := os.UserHomeDir()
		t = home + t[1:]
	}
	t = os.ExpandEnv(t)
	if !filepath.IsAbs(t) && cur != "" {
		t = filepath.Join(cur, t)
	}
	return filepath.Clean(t)
}

func shellOpen(target string) {
	t := cstr(target)
	defer C.free(unsafe.Pointer(t))
	C.sd_open_path(t)
}

func revealInFileManager(p string) {
	t := cstr(p)
	defer C.free(unsafe.Pointer(t))
	C.sd_reveal_path(t)
}

// moveToTrash moves files to the Trash.
func moveToTrash(w *Window, paths []string) error {
	for _, p := range paths {
		t := cstr(p)
		e := C.sd_trash_path(t)
		C.free(unsafe.Pointer(t))
		if e != nil {
			msg := C.GoString(e)
			C.free(unsafe.Pointer(e))
			return errors.New(msg)
		}
	}
	return nil
}

func pickFolder(owner *Window, title, initial string) (string, bool) {
	return pick(owner, title, initial, true)
}

func pickFile(owner *Window, title string) (string, bool) {
	return pick(owner, title, "", false)
}

func pick(owner *Window, title, initial string, folders bool) (string, bool) {
	t, i := cstr(title), cstr(initial)
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(i))
	var oh C.SDHandle
	if owner != nil {
		oh = owner.nh
	}
	theApp.modalDepth++
	r := C.sd_pick(oh, t, i, cbool(folders))
	theApp.modalDepth--
	if r == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(r))
	return C.GoString(r), true
}

const helpShortcut = "⌘?"

const shortcutHelp = "⌘R              Refresh\n" +
	"⌘U              Upload selected\n" +
	"⌘D              Download selected\n" +
	"F2              Rename\n" +
	"⌘⌫              Move to Trash / delete\n" +
	"⇧⌘N             New folder\n" +
	"⌘↑ or ⌫         Up one level\n" +
	"⌘[ / ⌘]         Back / Forward\n" +
	"⇧⌘G             Type a path\n" +
	"⌘F              Filter files\n" +
	"⌘S              Site Manager\n" +
	"⌘K              Quick Connect\n" +
	"⌘E              Disconnect\n" +
	"⌘Y              Synchronized browsing\n" +
	"⌘P / ⇧⌘P        Pause / resume all transfers\n" +
	"⌘T              Toggle light / dark\n" +
	"⇧⌘.             Show hidden files\n" +
	"⌘,              Settings"
const siteManagerKey = "⌘S"
