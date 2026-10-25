//go:build windows

package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"shoredrop/internal/core"
)

// Installer for ShoreDrop: a per-user install (no administrator rights)
// into %LOCALAPPDATA%\Programs\ShoreDrop with Start menu and desktop
// shortcuts and an entry in Settings > Apps.

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\ShoreDrop`

var (
	pRegCreateKeyExW = advapi.NewProc("RegCreateKeyExW")
	pRegSetValueExW  = advapi.NewProc("RegSetValueExW")
	pRegCloseKey     = advapi.NewProc("RegCloseKey")
	pRegDeleteTreeW  = advapi.NewProc("RegDeleteTreeW")

	clsidShellLink  = guid{0x00021401, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIShellLinkW  = guid{0x000214F9, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIPersistFile = guid{0x0000010b, 0, 0, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

func defaultInstallDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "Programs", "ShoreDrop")
}

func startMenuDir() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`)
}

func desktopDir() string {
	home, _ := os.UserHomeDir()
	// honour a relocated Desktop (e.g. OneDrive) via the known-folder API
	var p *uint16
	fid := guid{0xB4BFCC3A, 0xDB2C, 0x424C, [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41}} // FOLDERID_Desktop
	if r, _, _ := pSHGetKnownFolder.Call(uintptr(unsafe.Pointer(&fid)), 0, 0, uintptr(unsafe.Pointer(&p))); r == 0 && p != nil {
		defer pCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
		return syscall.UTF16ToString(unsafe.Slice(p, 1024))
	}
	return filepath.Join(home, "Desktop")
}

// createShortcut writes a .lnk via IShellLinkW / IPersistFile.
func createShortcut(lnk, target, workdir, desc string) error {
	var sl uintptr
	hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, 1, uintptr(unsafe.Pointer(&iidIShellLinkW)), uintptr(unsafe.Pointer(&sl)))
	if hr != 0 || sl == 0 {
		return fmt.Errorf("could not create shortcut (0x%x)", hr)
	}
	defer comRelease(sl)
	comCall(sl, 20, up(target))    // SetPath
	comCall(sl, 9, up(workdir))    // SetWorkingDirectory
	comCall(sl, 7, up(desc))       // SetDescription
	comCall(sl, 17, up(target), 0) // SetIconLocation
	var pf uintptr
	if comCall(sl, 0, uintptr(unsafe.Pointer(&iidIPersistFile)), uintptr(unsafe.Pointer(&pf))) != 0 || pf == 0 {
		return errors.New("could not save shortcut")
	}
	defer comRelease(pf)
	if r := comCall(pf, 6, up(lnk), 1); r != 0 { // Save
		return fmt.Errorf("could not save shortcut (0x%x)", r)
	}
	return nil
}

func regSetString(key uintptr, name, val string) {
	u, _ := syscall.UTF16FromString(val)
	pRegSetValueExW.Call(key, up(name), 0, 1 /*REG_SZ*/, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)*2))
}

func regSetDword(key uintptr, name string, v uint32) {
	pRegSetValueExW.Call(key, up(name), 0, 4 /*REG_DWORD*/, uintptr(unsafe.Pointer(&v)), 4)
}

func registerUninstall(dir, version string, sizeKB uint32) error {
	var key uintptr
	r, _, _ := pRegCreateKeyExW.Call(0x80000001, up(uninstallKey), 0, 0, 0, 0xF003F /*KEY_ALL_ACCESS*/, 0, uintptr(unsafe.Pointer(&key)), 0)
	if r != 0 {
		return fmt.Errorf("could not write the uninstall registry entry (%d)", r)
	}
	defer pRegCloseKey.Call(key)
	exe := filepath.Join(dir, "ShoreDrop.exe")
	un := filepath.Join(dir, "Uninstall ShoreDrop.exe")
	regSetString(key, "DisplayName", "ShoreDrop")
	regSetString(key, "DisplayVersion", version)
	regSetString(key, "Publisher", "ShoreDrop")
	regSetString(key, "DisplayIcon", exe+",0")
	regSetString(key, "InstallLocation", dir)
	regSetString(key, "UninstallString", `"`+un+`" /uninstall`)
	regSetString(key, "QuietUninstallString", `"`+un+`" /uninstall /S`)
	regSetDword(key, "NoModify", 1)
	regSetDword(key, "NoRepair", 1)
	regSetDword(key, "EstimatedSize", sizeKB)
	return nil
}

// install copies the payload and creates shortcuts.
func install(payload []byte, dir string, desktop bool, version string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exe := filepath.Join(dir, "ShoreDrop.exe")
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, payload, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		// the old version may still be running: move it aside first
		old := exe + ".old"
		os.Remove(old)
		if err2 := os.Rename(exe, old); err2 != nil {
			os.Remove(tmp)
			return errors.New("ShoreDrop is running. Close it and run Setup again")
		}
		if err := os.Rename(tmp, exe); err != nil {
			return err
		}
	}
	self, _ := os.Executable()
	if data, err := os.ReadFile(self); err == nil {
		os.WriteFile(filepath.Join(dir, "Uninstall ShoreDrop.exe"), data, 0o755)
	}
	if err := createShortcut(filepath.Join(startMenuDir(), "ShoreDrop.lnk"), exe, dir, "ShoreDrop FTP, FTPS and SFTP client"); err != nil {
		return err
	}
	lnk := filepath.Join(desktopDir(), "ShoreDrop.lnk")
	if desktop {
		createShortcut(lnk, exe, dir, "ShoreDrop FTP, FTPS and SFTP client")
	} else {
		os.Remove(lnk)
	}
	return registerUninstall(dir, version, uint32(len(payload)/1024*2))
}

func uninstall(dir string, removeData bool) error {
	// Remove the program first so nothing else is touched if it's still running.
	for _, f := range []string{"ShoreDrop.exe", "ShoreDrop.exe.old"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
			return errors.New("ShoreDrop is running. Close it and try again")
		}
	}
	os.Remove(filepath.Join(startMenuDir(), "ShoreDrop.lnk"))
	os.Remove(filepath.Join(desktopDir(), "ShoreDrop.lnk"))
	pRegDeleteTreeW.Call(0x80000001, up(uninstallKey))
	if removeData {
		sites := core.LoadSites()
		sites.ForgetAllPasswords()
		os.RemoveAll(core.ConfigDir())
	}
	// The uninstaller can't delete itself while running: remove the folder
	// shortly after this process exits.
	cmd := exec.Command("cmd.exe", "/C", "ping -n 3 127.0.0.1 >nul & rmdir /S /Q \""+dir+"\"")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /*CREATE_NO_WINDOW*/}
	cmd.Start()
	return nil
}

func hasArg(name string) bool {
	for _, a := range os.Args[1:] {
		if strings.EqualFold(a, name) || strings.EqualFold(a, "-"+strings.TrimPrefix(name, "/")) {
			return true
		}
	}
	return false
}

// RunSetup is the entry point of ShoreDrop-Setup.exe (and of the copied uninstaller).
func RunSetup(payload []byte, version string) {
	self, _ := os.Executable()
	uninstallMode := hasArg("/uninstall") || strings.EqualFold(filepath.Base(self), "Uninstall ShoreDrop.exe")
	silent := hasArg("/S")
	if silent {
		var err error
		if uninstallMode {
			err = uninstall(filepath.Dir(self), false)
		} else {
			err = install(payload, defaultInstallDir(), true, version)
		}
		if err != nil {
			os.Exit(1)
		}
		return
	}
	s := core.LoadSettings()
	a := newApp(&s)
	if uninstallMode {
		runUninstallUI(a, filepath.Dir(self))
	} else {
		runInstallUI(a, payload, version)
	}
}

func setupWindow(a *App, title string) *Dialog {
	w := a.newWindow(winOpts{title: title, w: 640, h: 470, dialog: true})
	a.main = w
	d := &Dialog{Window: w, Title: title, BigHeader: true, Result: -1, DefaultBtn: -1}
	d.closeBtn = &Button{Base: Base{W: w}, Icon: icChromeX, Style: BtnChromeClose, OnClick: w.Close}
	w.Add(d.closeBtn)
	w.Layout = d.layout
	w.PaintBg = d.paint
	w.OnKey = func(e *KeyEvent) bool {
		if e.VK == VK_ESCAPE {
			w.Close()
			return true
		}
		if e.VK == VK_RETURN && d.DefaultBtn >= 0 && !d.Buttons[d.DefaultBtn].Disabled && d.Buttons[d.DefaultBtn].IsVisible() {
			d.Buttons[d.DefaultBtn].OnClick()
			return true
		}
		return false
	}
	// centre on screen
	x, y, W, H := w.centerSize(nil, 640, 470)
	pSetWindowPos.Call(w.hwnd, 0, uintptr(x), uintptr(y), uintptr(W), uintptr(H), SWP_NOZORDER)
	return d
}

func runInstallUI(a *App, payload []byte, version string) {
	d := setupWindow(a, "Setup")
	w := d.Window
	dir := NewTextBox(w, "")
	dir.SetText(defaultInstallDir())
	browse := NewButton(w, "Browse…", BtnSecondary, func() {
		if f, ok := pickFolder(w, "Install ShoreDrop to", filepath.Dir(dir.Text())); ok {
			dir.SetText(filepath.Join(f, "ShoreDrop"))
		}
	})
	desk := NewCheckbox(w, "Create a desktop shortcut", true, nil)
	launch := NewCheckbox(w, "Launch ShoreDrop when setup finishes", true, nil)
	w.Add(dir, browse, desk, launch)
	state := 0 // 0 ready, 1 done, 2 error
	msg := ""
	d.FooterText = "ShoreDrop v" + version
	cancel := d.AddButton("Cancel", BtnSecondary, nil)
	var installBtn *Button
	installBtn = d.AddButton("Install", BtnPrimary, func() bool {
		if state == 1 {
			if launch.Checked {
				exe := filepath.Join(dir.Text(), "ShoreDrop.exe")
				cmd := exec.Command(exe)
				cmd.Dir = dir.Text()
				cmd.Start()
			}
			return true
		}
		target := strings.TrimSpace(dir.Text())
		if target == "" {
			return false
		}
		installBtn.Text = "Installing…"
		installBtn.Disabled = true
		w.Invalidate()
		pUpdateWindow.Call(w.hwnd)
		err := install(payload, target, desk.Checked, version)
		installBtn.Disabled = false
		if err != nil {
			state, msg = 2, err.Error()
			installBtn.Text = "Try again"
		} else {
			state, msg = 1, "ShoreDrop "+version+" was installed to "+target+". You'll find it in the Start menu"
			if desk.Checked {
				msg += " and on your desktop"
			}
			msg += ". To remove it later, use Settings › Apps › Installed apps."
			installBtn.Text = "Finish"
			cancel.Show(false)
			dir.Show(false)
			browse.Show(false)
			desk.Show(false)
		}
		d.layout()
		w.Invalidate()
		return false
	})
	d.DefaultBtn = 1
	d.LayoutBody = func(r Rect) {
		S := w.S
		dir.SetBounds(Rect{r.X + S(28), r.Y + S(138), r.W - S(56) - S(122), S(36)})
		browse.SetBounds(Rect{r.X + r.W - S(28) - S(110), r.Y + S(138), S(110), S(36)})
		desk.SetBounds(Rect{r.X + S(28), r.Y + S(190), r.W - S(56), S(30)})
		launch.SetBounds(Rect{r.X + S(28), r.Y + S(226), r.W - S(56), S(30)})
		if state == 1 {
			launch.SetBounds(Rect{r.X + S(28), r.Y + S(170), r.W - S(56), S(30)})
		}
	}
	d.PaintBody = func(c *Canvas, r Rect) {
		p := c.P
		S := w.S
		switch state {
		case 1:
			drawCheckCircle(c, r.X+S(28), r.Y+S(30), S(34), p.Success, p.Surface)
			c.Text("Installation complete", w.F.H1, p.TitleText, Rect{r.X + S(76), r.Y + S(26), r.W - S(100), S(40)}, AlignLeft)
			c.TextWrap(msg, w.F.Body, p.Text2, Rect{r.X + S(28), r.Y + S(84), r.W - S(56), S(80)})
		default:
			c.Text("Install ShoreDrop "+version, w.F.H1, p.TitleText, Rect{r.X + S(28), r.Y + S(22), r.W - S(56), S(40)}, AlignLeft)
			c.TextWrap("A modern FTP, FTPS and SFTP client. No administrator rights are needed — ShoreDrop installs just for you.",
				w.F.Body, p.Text2, Rect{r.X + S(28), r.Y + S(62), r.W - S(56), S(44)})
			c.Text("Install location", w.F.Label, p.Text, Rect{r.X + S(28), r.Y + S(112), S(300), S(22)}, AlignLeft)
			if state == 2 {
				c.TextWrap("⚠ "+msg, w.F.Body, p.Danger, Rect{r.X + S(28), r.Y + S(266), r.W - S(56), S(40)})
			}
		}
	}
	d.relayout()
	pShowWindow.Call(w.hwnd, SW_SHOW)
	pSetForegroundWindow.Call(w.hwnd)
	w.SetFocusWidget(installBtn)
	a.loop(nil)
}

func runUninstallUI(a *App, dir string) {
	d := setupWindow(a, "Uninstall")
	w := d.Window
	data := NewCheckbox(w, "Also remove my settings, saved sites and their passwords", false, nil)
	w.Add(data)
	done := false
	msg := ""
	d.FooterText = "ShoreDrop v" + core.AppVersion
	cancel := d.AddButton("Cancel", BtnSecondary, nil)
	var btn *Button
	btn = d.AddButton("Uninstall", BtnDanger, func() bool {
		if done {
			return true
		}
		if err := uninstall(dir, data.Checked); err != nil {
			msg = err.Error()
			w.Invalidate()
			return false
		}
		done = true
		btn.Text = "Close"
		btn.Style = BtnPrimary
		cancel.Show(false)
		data.Show(false)
		d.layout()
		w.Invalidate()
		return false
	})
	d.DefaultBtn = 1
	d.LayoutBody = func(r Rect) {
		data.SetBounds(Rect{r.X + w.S(28), r.Y + w.S(130), r.W - w.S(56), w.S(30)})
	}
	d.PaintBody = func(c *Canvas, r Rect) {
		p := c.P
		S := w.S
		if done {
			drawCheckCircle(c, r.X+S(28), r.Y+S(30), S(34), p.Success, p.Surface)
			c.Text("ShoreDrop was removed", w.F.H1, p.TitleText, Rect{r.X + S(76), r.Y + S(26), r.W - S(100), S(40)}, AlignLeft)
			c.TextWrap("Thanks for using ShoreDrop.", w.F.Body, p.Text2, Rect{r.X + S(28), r.Y + S(84), r.W - S(56), S(60)})
			return
		}
		c.Text("Uninstall ShoreDrop", w.F.H1, p.TitleText, Rect{r.X + S(28), r.Y + S(22), r.W - S(56), S(40)}, AlignLeft)
		c.TextWrap("This removes ShoreDrop from "+dir+" along with its shortcuts. Your saved sites and settings are kept unless you tick the box below.",
			w.F.Body, p.Text2, Rect{r.X + S(28), r.Y + S(64), r.W - S(56), S(60)})
		if msg != "" {
			c.TextWrap("⚠ "+msg, w.F.Body, p.Danger, Rect{r.X + S(28), r.Y + S(180), r.W - S(56), S(40)})
		}
	}
	d.relayout()
	pShowWindow.Call(w.hwnd, SW_SHOW)
	pSetForegroundWindow.Call(w.hwnd)
	a.loop(nil)
}
