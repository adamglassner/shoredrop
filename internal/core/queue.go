package core

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"shoredrop/internal/remote"
)

// Direction of a transfer.
type Direction int

const (
	Upload Direction = iota
	Download
)

// Status of a queue item.
type Status int

const (
	Queued Status = iota
	Active
	Paused
	Done
	Failed
	Cancelled
	Skipped
)

func (s Status) String() string {
	return [...]string{"Queued", "Transferring", "Paused", "Completed", "Failed", "Cancelled", "Skipped"}[s]
}

// Item is one file (or empty folder) in the transfer queue.
type Item struct {
	ID       int64
	Dir      Direction
	Local    string // local path (file)
	Remote   string // remote path (file)
	Size     int64
	ModTime  time.Time
	IsDir    bool // create an (empty) directory only
	Server   string
	Status   Status
	Err      string
	Speed    float64 // bytes per second (smoothed)
	Started  time.Time
	Finished time.Time

	done      atomic.Int64
	params    *remote.Params
	stopMode  atomic.Int32 // 0 none, 1 pause, 2 cancel
	stopCh    chan struct{}
	conn      remote.FS
	resume    bool
	remove    bool
	lastBytes int64
}

// Transferred returns the number of bytes moved so far.
func (it *Item) Transferred() int64 { return it.done.Load() }

// Name returns the file name shown in the queue.
func (it *Item) Name() string {
	if it.Dir == Upload {
		return filepath.Base(it.Local)
	}
	return path.Base(it.Remote)
}

// Progress returns 0..1.
func (it *Item) Progress() float64 {
	if it.Status == Done {
		return 1
	}
	if it.Size <= 0 {
		return 0
	}
	p := float64(it.done.Load()) / float64(it.Size)
	if p > 1 {
		p = 1
	}
	return p
}

// Remaining estimates time left.
func (it *Item) Remaining() time.Duration {
	if it.Speed <= 1 || it.Size <= 0 {
		return -1
	}
	left := it.Size - it.done.Load()
	if left < 0 {
		left = 0
	}
	return time.Duration(float64(left) / it.Speed * float64(time.Second))
}

type idleConn struct {
	fs    remote.FS
	since time.Time
}

// Queue runs transfers with a bounded number of parallel connections.
type Queue struct {
	mu        sync.Mutex
	items     []*Item
	nextID    int64
	active    int
	holdAll   bool
	idle      map[string][]idleConn
	dirCache  map[string]bool
	expanding int

	Max          func() int
	ExistsAction func() string
	ResumeOn     func() bool
	Log          *Log
	OnChange     func()

	totalSpeed float64
	history    []float64
}

// NewQueue creates a queue and starts its housekeeping ticker.
func NewQueue(log *Log) *Queue {
	q := &Queue{idle: map[string][]idleConn{}, dirCache: map[string]bool{}, Log: log,
		Max: func() int { return 2 }, ExistsAction: func() string { return "overwrite" }, ResumeOn: func() bool { return true }}
	go q.tick()
	return q
}

func paramsKey(p *remote.Params) string {
	return fmt.Sprintf("%s|%s|%d|%s|%s", p.Protocol, strings.ToLower(p.Host), p.Port, p.User, p.KeyFile)
}

func (q *Queue) changed() {
	if q.OnChange != nil {
		q.OnChange()
	}
}

// Items returns a snapshot of the queue.
func (q *Queue) Items() []*Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]*Item(nil), q.items...)
}

// Stats summarises the queue.
type Stats struct {
	Total, Active, Queued, Paused, Done, Failed int
	Bytes, BytesDone                            int64
	Speed                                       float64
	History                                     []float64
	Expanding                                   bool
}

func (q *Queue) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	var s Stats
	for _, it := range q.items {
		s.Total++
		switch it.Status {
		case Active:
			s.Active++
		case Queued:
			s.Queued++
		case Paused:
			s.Paused++
		case Done, Skipped:
			s.Done++
		case Failed:
			s.Failed++
		}
		if it.Status != Cancelled {
			s.Bytes += it.Size
			if it.Status == Done {
				s.BytesDone += it.Size
			} else {
				s.BytesDone += it.done.Load()
			}
		}
	}
	s.Speed = q.totalSpeed
	s.History = append([]float64(nil), q.history...)
	s.Expanding = q.expanding > 0
	return s
}

func (q *Queue) tick() {
	t := time.NewTicker(500 * time.Millisecond)
	last := time.Now()
	for now := range t.C {
		dt := now.Sub(last).Seconds()
		last = now
		q.mu.Lock()
		total := 0.0
		anyActive := false
		for _, it := range q.items {
			if it.Status != Active {
				continue
			}
			anyActive = true
			d := it.done.Load()
			inst := float64(d-it.lastBytes) / dt
			if inst < 0 {
				inst = 0
			}
			it.lastBytes = d
			if it.Speed == 0 {
				it.Speed = inst
			} else {
				it.Speed = it.Speed*0.65 + inst*0.35
			}
			total += it.Speed
		}
		q.totalSpeed = total
		if anyActive || len(q.history) > 0 && q.history[len(q.history)-1] > 0 {
			q.history = append(q.history, total)
			if len(q.history) > 120 {
				q.history = q.history[len(q.history)-120:]
			}
		}
		// Close connections that have been idle for a minute.
		var toClose []remote.FS
		for k, list := range q.idle {
			keep := list[:0]
			for _, ic := range list {
				if now.Sub(ic.since) > 60*time.Second {
					toClose = append(toClose, ic.fs)
				} else {
					keep = append(keep, ic)
				}
			}
			q.idle[k] = keep
		}
		q.mu.Unlock()
		for _, c := range toClose {
			go c.Close()
		}
		if anyActive {
			q.changed()
		}
	}
}

func (q *Queue) add(items []*Item) {
	q.mu.Lock()
	for _, it := range items {
		q.nextID++
		it.ID = q.nextID
		it.Status = Queued
		q.items = append(q.items, it)
	}
	q.dispatchLocked()
	q.mu.Unlock()
	q.changed()
}

// AddUpload queues a local file or folder for upload into remoteDir.
func (q *Queue) AddUpload(p remote.Params, local, remoteDir string) {
	pp := &p
	st, err := os.Stat(local)
	if err != nil {
		q.Log.Add(LogError, "Cannot upload %s: %v", local, err)
		return
	}
	target := path.Join(remoteDir, filepath.Base(local))
	if !st.IsDir() {
		q.add([]*Item{{Dir: Upload, Local: local, Remote: target, Size: st.Size(), ModTime: st.ModTime(), params: pp, Server: p.Host}})
		return
	}
	q.mu.Lock()
	q.expanding++
	q.mu.Unlock()
	go func() {
		defer func() {
			q.mu.Lock()
			q.expanding--
			q.mu.Unlock()
			q.changed()
		}()
		var batch []*Item
		filepath.WalkDir(local, func(fp string, d fs.DirEntry, err error) error {
			if err != nil {
				q.Log.Add(LogWarning, "Skipping %s: %v", fp, err)
				return nil
			}
			rel, _ := filepath.Rel(local, fp)
			rp := path.Join(target, filepath.ToSlash(rel))
			if d.IsDir() {
				entries, _ := os.ReadDir(fp)
				if len(entries) == 0 {
					batch = append(batch, &Item{Dir: Upload, Local: fp, Remote: rp, IsDir: true, params: pp, Server: p.Host})
				}
				return nil
			}
			info, err := d.Info()
			if err != nil || !info.Mode().IsRegular() {
				return nil
			}
			batch = append(batch, &Item{Dir: Upload, Local: fp, Remote: rp, Size: info.Size(), ModTime: info.ModTime(), params: pp, Server: p.Host})
			if len(batch) >= 200 {
				q.add(batch)
				batch = nil
			}
			return nil
		})
		if len(batch) > 0 {
			q.add(batch)
		}
	}()
}

// AddDownload queues a remote file or folder for download into localDir.
func (q *Queue) AddDownload(p remote.Params, e remote.Entry, remotePath, localDir string) {
	pp := &p
	target := filepath.Join(localDir, SafeLocalName(e.Name))
	if !e.IsDir {
		q.add([]*Item{{Dir: Download, Local: target, Remote: remotePath, Size: e.Size, ModTime: e.ModTime, params: pp, Server: p.Host}})
		return
	}
	q.mu.Lock()
	q.expanding++
	q.mu.Unlock()
	go func() {
		defer func() {
			q.mu.Lock()
			q.expanding--
			q.mu.Unlock()
			q.changed()
		}()
		conn, err := q.getConn(pp)
		if err != nil {
			q.Log.Add(LogError, "Cannot list %s: %v", remotePath, err)
			return
		}
		ok := true
		var batch []*Item
		var walk func(rdir, ldir string, depth int)
		walk = func(rdir, ldir string, depth int) {
			if depth > 64 {
				return
			}
			list, err := conn.List(rdir)
			if err != nil {
				q.Log.Add(LogError, "Cannot list %s: %v", rdir, err)
				ok = false
				return
			}
			if len(list) == 0 {
				batch = append(batch, &Item{Dir: Download, Local: ldir, Remote: rdir, IsDir: true, params: pp, Server: p.Host})
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			for _, it := range list {
				rp := path.Join(rdir, it.Name)
				lp := filepath.Join(ldir, SafeLocalName(it.Name))
				if it.IsDir {
					walk(rp, lp, depth+1)
					continue
				}
				batch = append(batch, &Item{Dir: Download, Local: lp, Remote: rp, Size: it.Size, ModTime: it.ModTime, params: pp, Server: p.Host})
				if len(batch) >= 200 {
					q.add(batch)
					batch = nil
				}
			}
		}
		walk(remotePath, target, 0)
		if ok {
			q.putConn(pp, conn)
		} else {
			conn.Close()
		}
		if len(batch) > 0 {
			q.add(batch)
		}
	}()
}

// SafeLocalName replaces characters Windows does not allow in file names.
func SafeLocalName(n string) string {
	r := strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "\\", "_", "|", "_", "?", "_", "*", "_")
	n = r.Replace(n)
	n = strings.TrimRight(n, " .")
	if n == "" {
		n = "_"
	}
	return n
}

func (q *Queue) dispatchLocked() {
	if q.holdAll {
		return
	}
	max := q.Max()
	for _, it := range q.items {
		if q.active >= max {
			return
		}
		if it.Status == Queued {
			it.Status = Active
			it.Err = ""
			it.Started = time.Now()
			it.Speed = 0
			it.lastBytes = it.done.Load()
			it.stopMode.Store(0)
			it.stopCh = make(chan struct{})
			q.active++
			go q.run(it)
		}
	}
}

// Dispatch starts queued transfers if there is capacity (e.g. after Max changed).
func (q *Queue) Dispatch() {
	q.mu.Lock()
	q.dispatchLocked()
	q.mu.Unlock()
}

func (q *Queue) getConn(p *remote.Params) (remote.FS, error) {
	key := paramsKey(p)
	for {
		q.mu.Lock()
		list := q.idle[key]
		if len(list) == 0 {
			q.mu.Unlock()
			break
		}
		ic := list[len(list)-1]
		q.idle[key] = list[:len(list)-1]
		q.mu.Unlock()
		if ic.fs.KeepAlive() == nil {
			return ic.fs, nil
		}
		ic.fs.Close()
	}
	pc := *p
	pc.Log = nil // keep the log readable: worker connections log only results
	return remote.Connect(pc)
}

func (q *Queue) putConn(p *remote.Params, c remote.FS) {
	q.mu.Lock()
	defer q.mu.Unlock()
	key := paramsKey(p)
	if len(q.idle[key]) >= 10 {
		go c.Close()
		return
	}
	q.idle[key] = append(q.idle[key], idleConn{c, time.Now()})
}

// CloseIdle closes all pooled connections.
func (q *Queue) CloseIdle() {
	q.mu.Lock()
	var all []remote.FS
	for k, list := range q.idle {
		for _, ic := range list {
			all = append(all, ic.fs)
		}
		delete(q.idle, k)
	}
	q.mu.Unlock()
	for _, c := range all {
		c.Close()
	}
}

var errSkipped = errors.New("skipped")

// testDelay slows transfers down in tests so they can be paused mid-way.
var testDelay time.Duration

func (it *Item) progress(n int) {
	it.done.Add(int64(n))
	if testDelay > 0 {
		time.Sleep(testDelay)
	}
}

func (q *Queue) run(it *Item) {
	conn, err := q.getConn(it.params)
	if err == nil {
		q.mu.Lock()
		it.conn = conn
		q.mu.Unlock()
		if it.stopMode.Load() != 0 {
			conn.Abort()
			err = errors.New("stopped")
		} else if it.Dir == Download {
			err = q.download(conn, it)
		} else {
			err = q.upload(conn, it)
		}
	}
	mode := it.stopMode.Load()
	q.mu.Lock()
	it.conn = nil
	it.Speed = 0
	it.Finished = time.Now()
	switch {
	case mode == 1:
		it.Status = Paused
		it.resume = true
	case mode == 2:
		it.Status = Cancelled
	case errors.Is(err, errSkipped):
		it.Status = Skipped
	case err != nil:
		it.Status = Failed
		it.Err = friendlyError(err)
	default:
		it.Status = Done
		it.done.Store(it.Size)
	}
	q.active--
	status := it.Status
	if it.remove {
		for i, x := range q.items {
			if x == it {
				q.items = append(q.items[:i], q.items[i+1:]...)
				break
			}
		}
	}
	q.mu.Unlock()

	if conn != nil {
		if mode != 0 || (err != nil && !errors.Is(err, errSkipped)) {
			conn.Abort()
			conn.Close()
		} else {
			q.putConn(it.params, conn)
		}
	}
	verb := "Upload"
	src, dst := it.Local, it.Remote
	if it.Dir == Download {
		verb = "Download"
		src, dst = it.Remote, it.Local
	}
	switch status {
	case Done:
		if it.IsDir {
			q.Log.Add(LogSuccess, "Created folder %s", dst)
		} else {
			q.Log.Add(LogSuccess, "%s complete: %s → %s (%s)", verb, src, dst, FormatSize(it.Size))
		}
	case Failed:
		q.Log.Add(LogError, "%s failed: %s — %s", verb, src, it.Err)
	case Skipped:
		q.Log.Add(LogInfo, "Skipped %s (target already exists)", src)
	case Paused:
		q.Log.Add(LogInfo, "Paused %s at %s", it.Name(), FormatSize(it.done.Load()))
	case Cancelled:
		q.Log.Add(LogWarning, "Cancelled %s", it.Name())
	}
	q.mu.Lock()
	q.dispatchLocked()
	q.mu.Unlock()
	q.changed()
}

func friendlyError(err error) string {
	s := err.Error()
	if strings.Contains(s, "no such file") || strings.Contains(s, "550") {
		return s
	}
	return s
}

func (q *Queue) wantResume(it *Item) bool {
	return it.resume || q.ExistsAction() == "resume"
}

func (q *Queue) download(conn remote.FS, it *Item) error {
	if it.IsDir {
		return os.MkdirAll(it.Local, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(it.Local), 0o755); err != nil {
		return err
	}
	var offset int64
	if st, err := os.Stat(it.Local); err == nil {
		if st.IsDir() {
			return fmt.Errorf("a folder named %s already exists", filepath.Base(it.Local))
		}
		if q.wantResume(it) && q.ResumeOn() {
			if st.Size() == it.Size && it.Size > 0 {
				return nil
			}
			if st.Size() < it.Size {
				offset = st.Size()
			}
		} else if q.ExistsAction() == "skip" && !it.resume {
			return errSkipped
		}
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if offset > 0 {
		flags = os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(it.Local, flags, 0o644)
	if err != nil {
		return err
	}
	it.done.Store(offset)
	it.lastBytes = offset
	err = conn.Download(it.Remote, offset, f, it.stopCh, it.progress)
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if !it.ModTime.IsZero() {
		os.Chtimes(it.Local, time.Now(), it.ModTime)
	}
	return nil
}

func (q *Queue) ensureRemoteDir(conn remote.FS, p *remote.Params, dir string) error {
	if dir == "/" || dir == "." || dir == "" {
		return nil
	}
	key := paramsKey(p) + "|" + dir
	q.mu.Lock()
	ok := q.dirCache[key]
	q.mu.Unlock()
	if ok {
		return nil
	}
	if st, err := conn.Stat(dir); err == nil && st.IsDir {
		q.mu.Lock()
		q.dirCache[key] = true
		q.mu.Unlock()
		return nil
	}
	if err := q.ensureRemoteDir(conn, p, path.Dir(dir)); err != nil {
		return err
	}
	if err := conn.Mkdir(dir); err != nil {
		if st, serr := conn.Stat(dir); serr != nil || !st.IsDir {
			return fmt.Errorf("cannot create folder %s: %w", dir, err)
		}
	}
	q.mu.Lock()
	q.dirCache[key] = true
	q.mu.Unlock()
	return nil
}

// ForgetDir clears cached knowledge about remote folders (after deletes).
func (q *Queue) ForgetDirs() {
	q.mu.Lock()
	q.dirCache = map[string]bool{}
	q.mu.Unlock()
}

func (q *Queue) upload(conn remote.FS, it *Item) error {
	if it.IsDir {
		return q.ensureRemoteDir(conn, it.params, it.Remote)
	}
	if err := q.ensureRemoteDir(conn, it.params, path.Dir(it.Remote)); err != nil {
		return err
	}
	f, err := os.Open(it.Local)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	it.Size = st.Size()
	var offset int64
	if q.wantResume(it) && q.ResumeOn() || q.ExistsAction() == "skip" && !it.resume {
		if rs, err := conn.Stat(it.Remote); err == nil {
			if q.wantResume(it) && q.ResumeOn() {
				if rs.Size == it.Size && it.Size > 0 {
					return nil
				}
				if rs.Size < it.Size {
					offset = rs.Size
				}
			} else {
				return errSkipped
			}
		}
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	it.done.Store(offset)
	it.lastBytes = offset
	return conn.Upload(it.Remote, offset, f, it.stopCh, it.progress)
}

func (q *Queue) find(ids []int64) []*Item {
	set := map[int64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	var out []*Item
	for _, it := range q.items {
		if set[it.ID] {
			out = append(out, it)
		}
	}
	return out
}

func (q *Queue) stop(it *Item, mode int32) {
	if it.Status == Active {
		if it.stopMode.CompareAndSwap(0, mode) {
			if it.stopCh != nil {
				close(it.stopCh)
			}
			if it.conn != nil {
				go it.conn.Abort()
			}
		}
	}
}

// Pause pauses the given items (active ones are interrupted and can resume).
func (q *Queue) Pause(ids ...int64) {
	q.mu.Lock()
	for _, it := range q.find(ids) {
		switch it.Status {
		case Queued:
			it.Status = Paused
		case Active:
			q.stop(it, 1)
		}
	}
	q.mu.Unlock()
	q.changed()
}

// Resume re-queues paused items; they continue where they stopped.
func (q *Queue) Resume(ids ...int64) {
	q.mu.Lock()
	for _, it := range q.find(ids) {
		if it.Status == Paused {
			it.Status = Queued
			it.resume = true
		}
	}
	q.holdAll = false
	q.dispatchLocked()
	q.mu.Unlock()
	q.changed()
}

// Retry re-queues failed or cancelled items.
func (q *Queue) Retry(ids ...int64) {
	q.mu.Lock()
	for _, it := range q.find(ids) {
		if it.Status == Failed || it.Status == Cancelled {
			it.Status = Queued
			it.Err = ""
			it.resume = q.ResumeOn()
		}
	}
	q.dispatchLocked()
	q.mu.Unlock()
	q.changed()
}

// Cancel stops items permanently.
func (q *Queue) Cancel(ids ...int64) {
	q.mu.Lock()
	for _, it := range q.find(ids) {
		switch it.Status {
		case Queued, Paused:
			it.Status = Cancelled
		case Active:
			q.stop(it, 2)
		}
	}
	q.mu.Unlock()
	q.changed()
}

// Remove deletes items from the list, cancelling active ones.
func (q *Queue) Remove(ids ...int64) {
	q.mu.Lock()
	rm := map[int64]bool{}
	for _, it := range q.find(ids) {
		if it.Status == Active {
			it.remove = true
			q.stop(it, 2)
			continue // removed once it stops
		}
		rm[it.ID] = true
	}
	keep := q.items[:0]
	for _, it := range q.items {
		if !rm[it.ID] {
			keep = append(keep, it)
		}
	}
	q.items = keep
	q.mu.Unlock()
	q.changed()
}

// ClearFinished removes completed, skipped and cancelled items.
func (q *Queue) ClearFinished() {
	q.mu.Lock()
	keep := q.items[:0]
	for _, it := range q.items {
		if it.Status != Done && it.Status != Skipped && it.Status != Cancelled {
			keep = append(keep, it)
		}
	}
	q.items = keep
	q.mu.Unlock()
	q.changed()
}

func (q *Queue) allIDs(pred func(*Item) bool) []int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	var ids []int64
	for _, it := range q.items {
		if pred(it) {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

// PauseAll pauses everything that is queued or running.
func (q *Queue) PauseAll() {
	q.Pause(q.allIDs(func(it *Item) bool { return it.Status == Active || it.Status == Queued })...)
}

// ResumeAll resumes every paused item.
func (q *Queue) ResumeAll() {
	q.Resume(q.allIDs(func(it *Item) bool { return it.Status == Paused })...)
}

// RetryFailed retries all failed items.
func (q *Queue) RetryFailed() {
	q.Retry(q.allIDs(func(it *Item) bool { return it.Status == Failed })...)
}

// CancelAll cancels everything not yet finished.
func (q *Queue) CancelAll() {
	q.Cancel(q.allIDs(func(it *Item) bool { return it.Status == Active || it.Status == Queued || it.Status == Paused })...)
}

// FormatSize renders a byte count.
func FormatSize(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case f < k*k:
		return fmt.Sprintf("%.1f KB", f/k)
	case f < k*k*k:
		return fmt.Sprintf("%.1f MB", f/k/k)
	default:
		return fmt.Sprintf("%.2f GB", f/k/k/k)
	}
}

// FormatSpeed renders bytes/second.
func FormatSpeed(bps float64) string {
	if bps < 1 {
		return "—"
	}
	return FormatSize(int64(bps)) + "/s"
}

// FormatDuration renders a remaining-time estimate.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "—"
	}
	s := int(d.Seconds() + 0.5)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%d:%02d", s/60, s%60)
	default:
		return fmt.Sprintf("%d:%02d:%02d", s/3600, (s/60)%60, s%60)
	}
}
