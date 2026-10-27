//go:build windows

package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	drivesName      = "This PC"
	drivesSub       = "Drives"
	trashName       = "Recycle Bin"
	fileManagerName = "Explorer"
	osName          = "Windows"
	hiddenFilesHint = "Files starting with a dot, and hidden Windows files"
)

// ---------------------------------------------------------------- local file system

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
		hidden := false
		if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
			hidden = d.FileAttributes&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
		}
		if strings.HasPrefix(e.Name(), "$") {
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

func localPerm(m os.FileMode, dir bool) string {
	b := []byte("-rw-rw-rw-")
	if m&0o200 == 0 {
		b[2], b[5], b[8] = '-', '-', '-'
	}
	if dir {
		b[0] = 'd'
		b[3], b[6], b[9] = 'x', 'x', 'x'
	}
	return string(b)
}

func drives() []FileRow {
	mask, _, _ := pGetLogicalDrives.Call()
	var rows []FileRow
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		t, _, _ := pGetDriveTypeW.Call(up(root))
		if t == 1 /*NO_ROOT_DIR*/ {
			continue
		}
		rows = append(rows, FileRow{Name: root, IsDir: true, IsDrive: true, Perm: driveLabel(root, t)})
	}
	return rows
}

func driveLabel(root string, t uintptr) string {
	buf := make([]uint16, 64)
	pGetVolumeInfoW.Call(up(root), uintptr(unsafe.Pointer(&buf[0])), 64, 0, 0, 0, 0, 0)
	if l := syscall.UTF16ToString(buf); l != "" {
		return l
	}
	switch t {
	case 2:
		return "Removable"
	case 4:
		return "Network"
	case 5:
		return "CD/DVD"
	}
	return "Local Disk"
}

func parentLocal(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	clean := filepath.Clean(dir)
	if len(clean) <= 3 && strings.HasSuffix(clean, `:\`) || len(clean) == 2 && clean[1] == ':' {
		return "", true // drive root → This PC
	}
	if strings.HasPrefix(clean, `\\`) {
		// UNC: \\server\share is the top
		parts := strings.Split(strings.TrimPrefix(clean, `\\`), `\`)
		if len(parts) <= 2 {
			return "", true
		}
	}
	par := filepath.Dir(clean)
	if par == clean {
		return "", true
	}
	return par, true
}

func normalizeLocal(input, cur string) string {
	t := strings.Trim(strings.TrimSpace(input), `"`)
	if t == "" || strings.EqualFold(t, drivesName) {
		return ""
	}
	if strings.HasPrefix(t, "~") {
		home, _ := os.UserHomeDir()
		t = home + t[1:]
	}
	t = expandWinEnv(t)
	if len(t) == 2 && t[1] == ':' {
		t += `\`
	}
	if !filepath.IsAbs(t) && cur != "" {
		t = filepath.Join(cur, t)
	}
	return filepath.Clean(t)
}

// expandWinEnv expands %NAME% environment variables.
func expandWinEnv(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		name := s[i+1 : i+1+j]
		if v, ok := os.LookupEnv(name); ok && name != "" {
			b.WriteString(s[:i])
			b.WriteString(v)
		} else {
			b.WriteString(s[:i+j+2])
		}
		s = s[i+j+2:]
	}
}

func shellOpen(target string) {
	pShellExecuteW.Call(0, up("open"), up(target), 0, 0, SW_SHOWNORMAL)
}

// moveToTrash moves files to the Recycle Bin.
func moveToTrash(w *Window, paths []string) error {
	hwnd := w.hwnd
	var buf []uint16
	for _, p := range paths {
		u, _ := syscall.UTF16FromString(p)
		buf = append(buf, u...)
	}
	buf = append(buf, 0)
	op := struct {
		Hwnd    uintptr
		Func    uint32
		From    *uint16
		To      *uint16
		Flags   uint16
		Aborted int32
		Maps    uintptr
		Title   *uint16
	}{Hwnd: hwnd, Func: 3 /*FO_DELETE*/, From: &buf[0], Flags: 0x40 | 0x10 | 0x4 /*ALLOWUNDO|NOCONFIRMATION|SILENT*/}
	r, _, _ := pSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return fmt.Errorf("Windows could not delete the item (error %d)", r)
	}
	return nil
}

func revealInFileManager(p string) {
	pShellExecuteW.Call(0, up("open"), up("explorer.exe"), up("/select,\""+p+"\""), 0, SW_SHOWNORMAL)
}

const (
	nativeChrome  = false
	nativeMenuBar = false
)

func (m *MainWindow) setupNativeMenus() {}

const helpShortcut = "F1"

const shortcutHelp = "F5 / Ctrl+R    Refresh\n" +
	"Ctrl+U         Upload selected\n" +
	"Ctrl+D         Download selected\n" +
	"F2             Rename\n" +
	"Del            Delete\n" +
	"F7             New folder\n" +
	"Backspace      Up one level\n" +
	"Alt+← / Alt+→  Back / Forward\n" +
	"Ctrl+L         Type a path\n" +
	"Ctrl+F         Filter files\n" +
	"Ctrl+S         Site Manager\n" +
	"Ctrl+Q         Quick Connect\n" +
	"Ctrl+W         Disconnect\n" +
	"Ctrl+Y         Synchronized browsing\n" +
	"Ctrl+P         Pause all transfers\n" +
	"Ctrl+T         Toggle light / dark\n" +
	"Ctrl+,         Settings"
const siteManagerKey = "Ctrl+S"
