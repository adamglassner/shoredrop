//go:build windows || darwin

package ui

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"shoredrop/internal/core"
	"shoredrop/internal/remote"
)

// ---------------------------------------------------------------- transfers

func (m *MainWindow) needConnection() bool {
	if m.sess == nil {
		m.setStatus(stWarn, "Connect to a server first (Quick Connect or Site Manager)")
		return false
	}
	return true
}

func (m *MainWindow) uploadSelected() {
	if !m.needConnection() {
		return
	}
	rows := m.local.list.Selected()
	if len(rows) == 0 {
		m.setStatus(stWarn, "Select files in the local pane to upload")
		return
	}
	m.uploadRows(rows, m.remote.dir)
}

func (m *MainWindow) downloadSelected() {
	if !m.needConnection() {
		return
	}
	rows := m.remote.list.Selected()
	if len(rows) == 0 {
		m.setStatus(stWarn, "Select files in the remote pane to download")
		return
	}
	m.downloadRows(rows, m.local.dir)
}

func (m *MainWindow) uploadRows(rows []FileRow, remoteDir string) {
	if !m.needConnection() || m.local.dir == "" {
		return
	}
	var paths []string
	for _, r := range rows {
		if r.IsParent || r.IsDrive {
			continue
		}
		paths = append(paths, filepath.Join(m.local.dir, r.Name))
	}
	m.uploadPaths(paths, remoteDir)
}

func (m *MainWindow) uploadPaths(paths []string, remoteDir string) {
	if len(paths) == 0 {
		return
	}
	for _, p := range paths {
		m.q.AddUpload(m.sess.params, p, remoteDir)
	}
	m.log.Add(core.LogInfo, "Queued %d item(s) for upload to %s", len(paths), remoteDir)
	m.tabs.Selected = 0
	m.layout()
}

func (m *MainWindow) downloadRows(rows []FileRow, localDir string) {
	if !m.needConnection() {
		return
	}
	if localDir == "" {
		m.app.Alert(m.w, MsgInfo, "Choose a local folder", "Open a folder in the local pane first — downloads are saved into the folder shown there.")
		return
	}
	n := 0
	for _, r := range rows {
		if r.IsParent {
			continue
		}
		e, ok := m.remote.entries[r.Name]
		if !ok {
			e = remote.Entry{Name: r.Name, Size: r.Size, IsDir: r.IsDir, ModTime: r.ModTime}
		}
		m.q.AddDownload(m.sess.params, e, path.Join(m.remote.dir, r.Name), localDir)
		n++
	}
	if n > 0 {
		m.log.Add(core.LogInfo, "Queued %d item(s) for download to %s", n, localDir)
		m.tabs.Selected = 0
		m.layout()
	}
}

// queueChanged refreshes panes whose folders received finished transfers.
func (m *MainWindow) queueChanged() {
	for _, it := range m.q.Items() {
		prev, seen := m.seen[it.ID]
		m.seen[it.ID] = it.Status
		if it.Status != core.Done || (seen && prev == core.Done) {
			continue
		}
		if it.Dir == core.Download {
			d := filepath.Dir(it.Local)
			if it.IsDir {
				d = filepath.Dir(it.Local)
			}
			if strings.EqualFold(d, m.local.dir) || strings.HasPrefix(strings.ToLower(it.Local), strings.ToLower(m.local.dir)+`\`) {
				m.refreshLocal = true
			}
		} else if m.sess != nil {
			if strings.HasPrefix(it.Remote, strings.TrimSuffix(m.remote.dir, "/")+"/") {
				m.refreshRemote = true
			}
		}
	}
	if (m.refreshLocal || m.refreshRemote) && m.refreshTimer == 0 {
		m.refreshTimer = m.w.SetTimer(600*1e6, func() {
			m.w.KillTimer(m.refreshTimer)
			m.refreshTimer = 0
			if m.refreshLocal {
				m.refreshLocal = false
				m.local.refresh()
			}
			if m.refreshRemote {
				m.refreshRemote = false
				m.remote.refresh()
			}
		})
	}
	st := m.q.Stats()
	if st.Active > 0 {
		var cur *core.Item
		for _, it := range m.q.Items() {
			if it.Status == core.Active {
				cur = it
				break
			}
		}
		if cur != nil {
			verb := "Uploading"
			if cur.Dir == core.Download {
				verb = "Downloading"
			}
			m.setStatus(stBusy, "%s %s — %d%%", verb, cur.Name(), int(cur.Progress()*100))
		}
	} else if m.statusKind == stBusy && !m.connecting {
		if st.Failed > 0 {
			m.setStatus(stWarn, "Transfers finished — %d failed (see the queue)", st.Failed)
		} else if m.sess != nil {
			m.setStatus(stOK, "Transfer complete — Connected to %s (%s)", m.sess.params.Host, remote.ProtocolLabel(m.sess.params.Protocol))
		} else {
			m.setStatus(stOK, "Transfer complete")
		}
	}
	m.w.Invalidate()
}

// ---------------------------------------------------------------- drag & drop

func (m *MainWindow) beginDrag(p *Pane) bool {
	rows := p.list.Selected()
	if len(rows) == 0 {
		return false
	}
	m.drag = &dragState{from: p, rows: rows, row: -1}
	return true
}

func (m *MainWindow) paneAt(x, y int) *Pane {
	for _, p := range []*Pane{m.local, m.remote} {
		if p.rect.Contains(x, y) {
			return p
		}
	}
	return nil
}

func (m *MainWindow) dragMove(x, y int) {
	d := m.drag
	if d == nil {
		return
	}
	d.x, d.y = x, y
	for _, p := range []*Pane{m.local, m.remote} {
		p.list.DropHighlight = false
		p.list.DropRow = -1
	}
	d.target = m.paneAt(x, y)
	d.row = -1
	if t := d.target; t != nil {
		if t.isRemote && !m.connected() || !t.isRemote && t.dir == "" {
			d.target = nil
		} else {
			i := t.list.rowAt(y)
			if i >= 0 && t.list.R.Contains(x, y) {
				r := t.list.rows[i]
				dragged := false
				for _, dr := range d.rows {
					if dr.Name == r.Name {
						dragged = true
					}
				}
				if r.IsDir && !r.IsParent && !(t == d.from && dragged) {
					d.row = i
				}
			}
			if t != d.from || d.row >= 0 {
				t.list.DropHighlight = true
				t.list.DropRow = d.row
			}
		}
	}
	m.w.Invalidate()
}

func (m *MainWindow) dragEnd(x, y int, cancel bool) {
	d := m.drag
	m.drag = nil
	for _, p := range []*Pane{m.local, m.remote} {
		p.list.DropHighlight = false
		p.list.DropRow = -1
	}
	m.w.Invalidate()
	if d == nil || cancel || d.target == nil {
		return
	}
	t := d.target
	targetDir := t.dir
	if d.row >= 0 {
		targetDir = t.childPath(t.list.rows[d.row].Name)
	}
	switch {
	case t != d.from && t.isRemote:
		m.uploadRows(d.rows, targetDir)
	case t != d.from && !t.isRemote:
		m.downloadRows(d.rows, targetDir)
	case d.row >= 0:
		m.moveInto(t, d.rows, targetDir)
	}
}

// moveInto moves rows into a sub-folder within the same pane.
func (m *MainWindow) moveInto(p *Pane, rows []FileRow, dir string) {
	if p.isRemote {
		names := rowNames(rows)
		from := p.dir
		m.sess.Do(func(fs remote.FS) error {
			for _, n := range names {
				if err := fs.Rename(path.Join(from, n), path.Join(dir, n)); err != nil {
					return fmt.Errorf("%s: %w", n, err)
				}
			}
			return nil
		}, func(err error) {
			if err != nil {
				m.opFailed("Move failed", err)
			} else {
				m.log.Add(core.LogSuccess, "Moved %d item(s) into %s", len(names), dir)
			}
			p.refresh()
		})
		return
	}
	for _, r := range rows {
		if err := os.Rename(filepath.Join(p.dir, r.Name), filepath.Join(dir, r.Name)); err != nil {
			m.opFailed("Move failed", err)
			break
		}
	}
	p.refresh()
}

func (m *MainWindow) onDropFiles(files []string, x, y int) {
	p := m.paneAt(x, y)
	if p == nil {
		return
	}
	if !p.isRemote {
		m.setStatus(stWarn, "Drop files on the remote pane to upload them")
		return
	}
	if !m.needConnection() {
		return
	}
	dir := p.dir
	if i := p.list.rowAt(y); i >= 0 && p.list.rows[i].IsDir && !p.list.rows[i].IsParent {
		dir = p.childPath(p.list.rows[i].Name)
	}
	m.uploadPaths(files, dir)
}

// ---------------------------------------------------------------- file operations

func rowNames(rows []FileRow) []string {
	var out []string
	for _, r := range rows {
		if !r.IsParent && !r.IsDrive {
			out = append(out, r.Name)
		}
	}
	return out
}

func (m *MainWindow) opFailed(what string, err error) {
	msg := friendlyErr(err)
	m.log.Add(core.LogError, "%s: %s", what, msg)
	m.setStatus(stError, "%s: %s", what, msg)
	m.app.Alert(m.w, MsgError, what, msg)
}

func (m *MainWindow) newFolder(p *Pane) {
	if p.isRemote && !m.needConnection() || !p.isRemote && p.dir == "" {
		return
	}
	name, ok := m.app.Prompt(m.w, "New folder", "Name of the new folder in "+p.displayPath()+":", "New folder", false)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return
	}
	if p.isRemote {
		target := path.Join(p.dir, name)
		m.sess.Do(func(fs remote.FS) error { return fs.Mkdir(target) }, func(err error) {
			if err != nil {
				m.opFailed("Could not create folder", err)
				return
			}
			m.log.Add(core.LogSuccess, "Created folder %s", target)
			p.pendingSelect = name
			p.refresh()
		})
		return
	}
	if err := os.Mkdir(filepath.Join(p.dir, name), 0o755); err != nil {
		m.opFailed("Could not create folder", err)
		return
	}
	p.pendingSelect = name
	p.refresh()
}

func (m *MainWindow) rename(p *Pane) {
	sel := p.list.Selected()
	if len(sel) != 1 || sel[0].IsDrive {
		m.setStatus(stWarn, "Select one item to rename")
		return
	}
	m.w.SetFocusWidget(p.list)
	p.list.BeginRename(sel[0].Name)
}

func (m *MainWindow) doRename(p *Pane, oldName, newName string) {
	if strings.ContainsAny(newName, `/\`) {
		m.app.Alert(m.w, MsgError, "Invalid name", "Names can't contain / or \\.")
		return
	}
	if p.isRemote {
		from, to := path.Join(p.dir, oldName), path.Join(p.dir, newName)
		m.sess.Do(func(fs remote.FS) error { return fs.Rename(from, to) }, func(err error) {
			if err != nil {
				m.opFailed("Rename failed", err)
			} else {
				m.log.Add(core.LogSuccess, "Renamed %s → %s", from, to)
			}
			p.pendingSelect = newName
			p.refresh()
		})
		return
	}
	if err := os.Rename(filepath.Join(p.dir, oldName), filepath.Join(p.dir, newName)); err != nil {
		m.opFailed("Rename failed", err)
	}
	p.pendingSelect = newName
	p.refresh()
}

func (m *MainWindow) deleteSelected(p *Pane) {
	rows := p.list.Selected()
	names := rowNames(rows)
	if len(names) == 0 {
		return
	}
	what := names[0]
	if len(names) > 1 {
		what = fmt.Sprintf("%d items", len(names))
	}
	hasDir := false
	for _, r := range rows {
		hasDir = hasDir || r.IsDir
	}
	if p.isRemote {
		if !m.needConnection() {
			return
		}
		text := "This permanently deletes " + what + " from the server. It can't be undone."
		if hasDir {
			text = "This permanently deletes " + what + " from the server, including everything inside the selected folders. It can't be undone."
		}
		if m.S.ConfirmDelete && !m.app.Confirm(m.w, "Delete "+what+"?", text, "Delete", true) {
			return
		}
		dir := p.dir
		m.setStatus(stBusy, "Deleting %s…", what)
		m.sess.Do(func(fs remote.FS) error {
			for _, r := range rows {
				full := path.Join(dir, r.Name)
				var err error
				if r.IsDir && !r.IsLink {
					err = removeRemoteTree(fs, full, m.log)
				} else {
					err = fs.Remove(full)
				}
				if err != nil {
					return fmt.Errorf("%s: %w", r.Name, err)
				}
				m.log.Add(core.LogSuccess, "Deleted %s", full)
			}
			return nil
		}, func(err error) {
			m.q.ForgetDirs()
			if err != nil {
				m.opFailed("Delete failed", err)
			} else {
				m.setStatus(stOK, "Deleted %s", what)
			}
			p.refresh()
		})
		return
	}
	if p.dir == "" {
		return
	}
	if m.S.ConfirmDelete && !m.app.Confirm(m.w, "Delete "+what+"?", what+" will be moved to the "+trashName+".", "Move to "+trashName, true) {
		return
	}
	var full []string
	for _, n := range names {
		full = append(full, filepath.Join(p.dir, n))
	}
	if err := moveToTrash(m.w, full); err != nil {
		m.opFailed("Delete failed", err)
	} else {
		m.log.Add(core.LogSuccess, "Moved %s to the %s", what, trashName)
	}
	p.refresh()
}

func removeRemoteTree(fs remote.FS, dir string, log *core.Log) error {
	list, err := fs.List(dir)
	if err != nil {
		return err
	}
	for _, e := range list {
		full := path.Join(dir, e.Name)
		if e.IsDir && !e.IsLink {
			if err := removeRemoteTree(fs, full, log); err != nil {
				return err
			}
		} else if err := fs.Remove(full); err != nil {
			return err
		}
	}
	return fs.Rmdir(dir)
}

func (m *MainWindow) chmodSelected() {
	p := m.remote
	if !m.needConnection() {
		return
	}
	rows := p.list.Selected()
	names := rowNames(rows)
	if len(names) == 0 {
		m.setStatus(stWarn, "Select remote files or folders to change their permissions")
		return
	}
	mode := uint32(0o644)
	hasDir := false
	for _, r := range rows {
		if r.IsDir {
			hasDir = true
		}
	}
	if rows[0].HasMode {
		mode = rows[0].Mode
	} else if rows[0].IsDir {
		mode = 0o755
	}
	newMode, recursive, ok := m.app.ChmodDialog(m.w, names, mode, hasDir)
	if !ok {
		return
	}
	dir := p.dir
	m.sess.Do(func(fs remote.FS) error {
		var apply func(full string, isDir bool) error
		apply = func(full string, isDir bool) error {
			if err := fs.Chmod(full, newMode); err != nil {
				return fmt.Errorf("%s: %w", full, err)
			}
			if isDir && recursive {
				list, err := fs.List(full)
				if err != nil {
					return err
				}
				for _, e := range list {
					if err := apply(path.Join(full, e.Name), e.IsDir && !e.IsLink); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for _, r := range rows {
			if err := apply(path.Join(dir, r.Name), r.IsDir); err != nil {
				return err
			}
		}
		return nil
	}, func(err error) {
		if err != nil {
			m.opFailed("Could not change permissions", err)
		} else {
			m.log.Add(core.LogSuccess, "Permissions set to %03o on %d item(s)", newMode, len(names))
			m.setStatus(stOK, "Permissions changed to %03o", newMode)
		}
		p.refresh()
	})
}

// ---------------------------------------------------------------- context menus

func (m *MainWindow) paneContext(p *Pane, x, y int) {
	m.active = p
	sel := p.list.Selected()
	one := len(sel) == 1
	any := len(sel) > 0
	var items []MenuItem
	if p.isRemote {
		if !m.connected() {
			items = []MenuItem{{Label: "Site Manager…", Icon: icServer, Action: m.openSiteManager}, {Label: "Quick Connect", Icon: icPlug, Action: m.focusQuickConnect}}
		} else {
			items = []MenuItem{
				{Label: "Download", Shortcut: "Ctrl+D", Icon: icDownload, Disabled: !any || m.local.dir == "", Action: m.downloadSelected},
			}
			if one && sel[0].IsDir {
				items = append(items, MenuItem{Label: "Open", Icon: icOpen, Action: func() { p.open(sel[0]) }})
			}
			items = append(items, Sep,
				MenuItem{Label: "Create Folder…", Shortcut: "F7", Icon: icFolderAdd, Action: func() { m.newFolder(p) }},
				MenuItem{Label: "Rename", Shortcut: "F2", Icon: icRename, Disabled: !one, Action: func() { m.rename(p) }},
				MenuItem{Label: "Delete", Shortcut: "Del", Icon: icDelete, Danger: true, Disabled: !any, Action: func() { m.deleteSelected(p) }},
				MenuItem{Label: "Change Permissions…", Icon: icPermissions, Disabled: !any, Action: m.chmodSelected},
				Sep,
				MenuItem{Label: "Copy URL", Icon: icCopy, Action: func() {
					t := m.sess.url(p.dir)
					if one {
						t = m.sess.url(path.Join(p.dir, sel[0].Name))
					}
					setClipboardText(t)
				}},
				MenuItem{Label: "Refresh", Shortcut: "F5", Icon: icRefresh, Action: p.refresh},
			)
		}
	} else {
		items = []MenuItem{
			{Label: "Upload", Shortcut: "Ctrl+U", Icon: icUpload, Disabled: !any || !m.connected() || p.dir == "", Action: m.uploadSelected},
		}
		if one {
			items = append(items, MenuItem{Label: "Open", Icon: icOpen, Action: func() {
				if sel[0].IsDir || sel[0].IsDrive {
					p.open(sel[0])
				} else {
					shellOpen(p.childPath(sel[0].Name))
				}
			}})
		}
		items = append(items, Sep,
			MenuItem{Label: "Create Folder…", Shortcut: "F7", Icon: icFolderAdd, Disabled: p.dir == "", Action: func() { m.newFolder(p) }},
			MenuItem{Label: "Rename", Shortcut: "F2", Icon: icRename, Disabled: !one || p.dir == "", Action: func() { m.rename(p) }},
			MenuItem{Label: "Delete", Shortcut: "Del", Icon: icDelete, Danger: true, Disabled: !any || p.dir == "", Action: func() { m.deleteSelected(p) }},
			Sep,
			MenuItem{Label: "Show in " + fileManagerName, Icon: icFolder, Disabled: p.dir == "", Action: func() {
				if one {
					revealInFileManager(p.childPath(sel[0].Name))
				} else {
					shellOpen(p.dir)
				}
			}},
			MenuItem{Label: "Copy Path", Icon: icCopy, Action: func() {
				t := p.dir
				if one {
					t = p.childPath(sel[0].Name)
				}
				setClipboardText(t)
			}},
			MenuItem{Label: "Refresh", Shortcut: "F5", Icon: icRefresh, Action: p.refresh},
		)
	}
	sx, sy := m.w.ScreenPos(x, y)
	m.app.ShowMenu(m.w, items, sx, sy, 0, nil)
}

func (m *MainWindow) queueMenu(x, y int) {
	ids := m.queueView.Selected()
	has := len(ids) > 0
	items := []MenuItem{
		{Label: "Pause", Icon: icPause, Disabled: !has, Action: func() { m.q.Pause(ids...) }},
		{Label: "Resume", Icon: icPlay, Disabled: !has, Action: func() { m.q.Resume(ids...) }},
		{Label: "Retry", Icon: icRetry, Disabled: !has, Action: func() { m.q.Retry(ids...) }},
		{Label: "Cancel", Icon: icClose, Disabled: !has, Action: func() { m.q.Cancel(ids...) }},
		{Label: "Remove from Queue", Shortcut: "Del", Icon: icDelete, Disabled: !has, Action: func() { m.q.Remove(ids...) }},
		Sep,
		{Label: "Pause All", Shortcut: "Ctrl+P", Action: m.q.PauseAll},
		{Label: "Resume All", Action: m.q.ResumeAll},
		{Label: "Retry All Failed", Action: m.q.RetryFailed},
		{Label: "Clear Finished", Action: m.q.ClearFinished},
	}
	sx, sy := m.w.ScreenPos(x, y)
	m.app.ShowMenu(m.w, items, sx, sy, 0, nil)
}

// saveSessionAsSite stores the current connection in the Site Manager.
func (m *MainWindow) saveSessionAsSite() {
	if m.sess == nil {
		return
	}
	p := m.sess.params
	name, ok := m.app.Prompt(m.w, "Save to Site Manager", "Name for this site:", p.Host, false)
	if !ok || strings.TrimSpace(name) == "" {
		return
	}
	s := core.Site{ID: core.NewID(), Name: strings.TrimSpace(name), Protocol: p.Protocol, Host: p.Host, Port: p.Port, User: p.User, Logon: core.LogonNormal, RemoteDir: m.remote.dir}
	if p.KeyFile != "" {
		s.Logon = core.LogonKey
		s.KeyFile = p.KeyFile
	} else if p.Password != "" {
		if err := core.Credentials.Set(s.CredentialTarget(), p.User, p.Password); err != nil {
			m.app.Alert(m.w, MsgError, "Could not save the password", err.Error())
			s.Logon = core.LogonAsk
		}
	} else {
		s.Logon = core.LogonAsk
	}
	m.sites.Upsert(s)
	if err := m.sites.Save(); err != nil {
		m.opFailed("Could not save sites", err)
		return
	}
	m.log.Add(core.LogSuccess, "Saved site %q (password stored in %s)", s.Name, core.CredentialStoreName)
	m.setStatus(stOK, "Saved %q to the Site Manager", s.Name)
}
