//go:build darwin

package ui

/*
#include <stdlib.h>
#include "mac_darwin.h"
*/
import "C"

import (
	"strings"
	"unsafe"
)

// macShortcuts translates the Windows shortcut labels used by the shared menu
// definitions into their macOS equivalents.
var macShortcuts = map[string]string{
	"Ctrl+S":       "Cmd+S",
	"Ctrl+Q":       "Cmd+K",
	"F7":           "Cmd+Shift+N",
	"Del":          "Cmd+Backspace",
	"Ctrl+,":       "Cmd+,",
	"Ctrl+T":       "Cmd+T",
	"F5":           "Cmd+R",
	"Ctrl+H":       "Cmd+Shift+.",
	"Ctrl+F":       "Cmd+F",
	"Ctrl+L":       "Cmd+Shift+G",
	"Ctrl+U":       "Cmd+U",
	"Ctrl+D":       "Cmd+D",
	"Ctrl+P":       "Cmd+P",
	"Ctrl+Shift+P": "Cmd+Shift+P",
	"Ctrl+W":       "Cmd+E",
	"Ctrl+Shift+R": "Cmd+Shift+R",
	"Ctrl+Y":       "Cmd+Y",
	"F1":           "Cmd+?",
	"Alt+F4":       "",
}

// macShortcut returns the macOS form of a Windows shortcut label.
func macShortcut(s string) string {
	if m, ok := macShortcuts[s]; ok {
		return m
	}
	return strings.Replace(s, "Ctrl+", "Cmd+", 1)
}

// keyEquivalent converts "Cmd+Shift+N" into an AppKit key equivalent and
// modifier mask.
func keyEquivalent(s string) (string, C.ulong) {
	if s == "" {
		return "", 0
	}
	var mods C.ulong
	parts := strings.Split(s, "+")
	key := parts[len(parts)-1]
	if key == "" && strings.HasSuffix(s, "++") {
		key = "+"
	}
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "Cmd":
			mods |= modCommand
		case "Shift":
			mods |= modShift
		case "Alt", "Option":
			mods |= modOption
		case "Control":
			mods |= modControl
		}
	}
	switch key {
	case "Backspace":
		key = "\b"
	case "Del":
		key = string(rune(0x7F))
	case "Up":
		key = string(rune(0xF700))
	case "Down":
		key = string(rune(0xF701))
	case "Left":
		key = string(rune(0xF702))
	case "Right":
		key = string(rune(0xF703))
	case "Return":
		key = "\r"
	default:
		if len(key) >= 2 && key[0] == 'F' {
			n := 0
			for _, ch := range key[1:] {
				n = n*10 + int(ch-'0')
			}
			key = string(rune(0xF704 + n - 1)) // NSF1FunctionKey...
		} else {
			key = strings.ToLower(key)
		}
	}
	return key, mods
}

const (
	popupTagBase = 9_000_000
	menuTagBase  = 100_000
)

// addItems appends items to a native menu, registering their actions under
// tags starting at base. Returns the tags used.
func (a *App) addItems(menu C.uintptr_t, items []MenuItem, base int, disableAll bool) []int {
	var tags []int
	lastSep := true
	for i, it := range items {
		if it.Separator {
			if lastSep || i == len(items)-1 {
				continue
			}
			C.sd_menu_add(menu, nil, nil, 0, 0, 4)
			lastSep = true
			continue
		}
		lastSep = false
		tag := base + i
		a.actions[tag] = it.Action
		tags = append(tags, tag)
		flags := C.int(0)
		if it.Checked {
			flags |= 1
		}
		if it.Disabled || it.Action == nil || disableAll {
			flags |= 2
		}
		key, mods := keyEquivalent(macShortcut(it.Shortcut))
		l, k := cstr(it.Label), cstr(key)
		C.sd_menu_add(menu, l, k, mods, C.long(tag), flags)
		C.free(unsafe.Pointer(l))
		C.free(unsafe.Pointer(k))
	}
	return tags
}

// ShowMenu opens a native context menu at screen position (sx, sy); onClose
// (optional) runs when it closes and the chosen action runs afterwards.
func (a *App) ShowMenu(owner *Window, items []MenuItem, sx, sy, minW int, onClose func()) *Window {
	title := cstr("")
	m := C.sd_menu_new(title)
	C.free(unsafe.Pointer(title))
	defer C.sd_menu_release(m)
	for t := range a.actions {
		if t >= popupTagBase {
			delete(a.actions, t)
		}
	}
	a.nextPopup = (a.nextPopup + 1000) % 1_000_000
	base := popupTagBase + a.nextPopup
	a.addItems(C.uintptr_t(m), items, base, false)
	x, y := owner.ClientPos(sx, sy)
	tag := int(C.sd_menu_popup(m, owner.nh, C.double(x), C.double(y), C.double(minW)))
	if onClose != nil {
		onClose()
	}
	if tag >= base {
		if fn := a.actions[tag]; fn != nil {
			delete(a.actions, tag)
			a.Post(fn)
		}
	}
	return nil
}

//export goMenuAction
func goMenuAction(tag C.long) {
	a := theApp
	if a == nil {
		return
	}
	defer recoverUI("menu")
	if a.modalDepth > 0 && int(tag) < popupTagBase {
		return // menu-bar commands wait while a dialog is open
	}
	if fn := a.actions[int(tag)]; fn != nil {
		if int(tag) >= popupTagBase {
			delete(a.actions, int(tag))
		}
		a.Post(fn)
	}
}

//export goMenuUpdate
func goMenuUpdate(index C.int, menu C.uintptr_t) {
	a := theApp
	if a == nil {
		return
	}
	defer recoverUI("menu update")
	i := int(index)
	for _, t := range a.menuTags[i] {
		delete(a.actions, t)
	}
	C.sd_menu_clear(menu)
	fn := a.menuFns[i]
	if fn == nil {
		return
	}
	// while a dialog is open its window gets the keys; the main window's
	// commands wait until it closes, as they do on Windows
	a.menuTags[i] = a.addItems(menu, fn(), menuTagBase*(i+1), a.modalDepth > 0)
}

// menuBarMenu adds a top-level menu to the menu bar.
func (a *App) menuBarMenu(title string, role int, index int, fn func() []MenuItem) C.SDHandle {
	t := cstr(title)
	m := C.sd_menu_new(t)
	C.free(unsafe.Pointer(t))
	if fn != nil {
		a.menuFns[index] = fn
	}
	C.sd_menubar_add(m, C.int(index), C.int(role))
	return m
}

func addStd(m C.SDHandle, label, selector, shortcut string) {
	key, mods := keyEquivalent(shortcut)
	l, s, k := cstr(label), cstr(selector), cstr(key)
	C.sd_menu_add_std(C.uintptr_t(m), l, s, k, mods)
	C.free(unsafe.Pointer(l))
	C.free(unsafe.Pointer(s))
	C.free(unsafe.Pointer(k))
}

func addSep(m C.SDHandle) { C.sd_menu_add(C.uintptr_t(m), nil, nil, 0, 0, 4) }

// setupNativeMenus installs the macOS menu bar: the application menu, Edit,
// the ShoreDrop menus (File, View, Transfer, Bookmarks), Window and Help.
func (m *MainWindow) setupNativeMenus() {
	a := m.app
	app := a.menuBarMenu("ShoreDrop", 1, 0, nil)
	a.addItems(C.uintptr_t(app), []MenuItem{
		{Label: "About ShoreDrop", Action: func() { a.About(m.w) }},
		Sep,
		{Label: "Settings…", Shortcut: "Ctrl+,", Action: m.openSettings},
	}, 1, false)
	addSep(app)
	addStd(app, "Hide ShoreDrop", "hide:", "Cmd+H")
	addStd(app, "Hide Others", "hideOtherApplications:", "Cmd+Alt+H")
	addStd(app, "Show All", "unhideAllApplications:", "")
	addSep(app)
	addStd(app, "Quit ShoreDrop", "terminate:", "Cmd+Q")

	edit := a.menuBarMenu("Edit", 4, 0, nil)
	addStd(edit, "Undo", "undo:", "Cmd+Z")
	addStd(edit, "Redo", "redo:", "Cmd+Shift+Z")
	addSep(edit)
	addStd(edit, "Cut", "cut:", "Cmd+X")
	addStd(edit, "Copy", "copy:", "Cmd+C")
	addStd(edit, "Paste", "paste:", "Cmd+V")
	addStd(edit, "Select All", "selectAll:", "Cmd+A")

	for i, d := range m.menus {
		if d.title == "Help" {
			continue
		}
		items := d.items
		a.menuBarMenu(d.title, 0, i+1, func() []MenuItem { return macMenuItems(items()) })
	}

	win := a.menuBarMenu("Window", 2, 0, nil)
	addStd(win, "Minimize", "performMiniaturize:", "Cmd+M")
	addStd(win, "Zoom", "performZoom:", "")
	addStd(win, "Close Window", "performClose:", "Cmd+W")
	addSep(win)
	addStd(win, "Bring All to Front", "arrangeInFront:", "")

	for i, d := range m.menus {
		if d.title == "Help" {
			items := d.items
			a.menuBarMenu("Help", 3, i+1, func() []MenuItem { return macMenuItems(items()) })
		}
	}
}

// macMenuItems drops entries that live in the application menu on macOS.
func macMenuItems(items []MenuItem) []MenuItem {
	var out []MenuItem
	for _, it := range items {
		switch it.Label {
		case "Exit", "Settings…", "About ShoreDrop":
			continue
		}
		out = append(out, it)
	}
	return out
}
