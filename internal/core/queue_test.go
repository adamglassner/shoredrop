package core

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"shoredrop/internal/remote"
)

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func allFinished(q *Queue) bool {
	s := q.Stats()
	return s.Active == 0 && s.Queued == 0 && !s.Expanding
}

func testParams(t *testing.T) []remote.Params {
	var out []remote.Params
	if v := os.Getenv("SD_SSH_PORT"); v != "" {
		p, _ := strconv.Atoi(v)
		out = append(out, remote.Params{Protocol: remote.SFTP, Host: "127.0.0.1", Port: p, User: "tester", Password: "s3cret pass"})
	}
	if v := os.Getenv("SD_FTP_PORT"); v != "" {
		p, _ := strconv.Atoi(v)
		out = append(out, remote.Params{Protocol: remote.FTPS, Host: "127.0.0.1", Port: p, User: "tester", Password: "s3cret pass", Passive: true,
			CertCheck: nil})
	}
	if len(out) == 0 {
		t.Skip("no test servers")
	}
	return out
}

func TestQueueTreeRoundTrip(t *testing.T) {
	for _, p := range testParams(t) {
		p := p
		if p.Protocol == remote.FTPS {
			p.CertCheck = acceptAll
		}
		t.Run(p.Protocol, func(t *testing.T) {
			log := &Log{}
			q := NewQueue(log)
			q.Max = func() int { return 3 }
			src := t.TempDir()
			files := map[string][]byte{}
			for i, rel := range []string{"a.txt", "sub/b.bin", "sub/deeper/c.bin", "sub/deeper/d e.txt"} {
				data := make([]byte, 1000*(i+1)*37)
				rand.Read(data)
				files[rel] = data
				fp := filepath.Join(src, filepath.FromSlash(rel))
				os.MkdirAll(filepath.Dir(fp), 0o755)
				os.WriteFile(fp, data, 0o644)
			}
			os.MkdirAll(filepath.Join(src, "empty"), 0o755)
			conn, err := remote.Connect(p)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			rdir := path.Join(conn.Home(), "qt-"+strconv.FormatInt(time.Now().UnixNano(), 36))
			if err := conn.Mkdir(rdir); err != nil {
				t.Fatal(err)
			}
			q.AddUpload(p, src, rdir)
			waitFor(t, "uploads", 30*time.Second, func() bool { return allFinished(q) && q.Stats().Total > 0 })
			for _, it := range q.Items() {
				if it.Status != Done {
					t.Fatalf("upload item %s: %v %s", it.Remote, it.Status, it.Err)
				}
			}
			// download the uploaded tree again
			dst := t.TempDir()
			remoteRoot := path.Join(rdir, filepath.Base(src))
			q.ClearFinished()
			q.AddDownload(p, remote.Entry{Name: filepath.Base(src), IsDir: true}, remoteRoot, dst)
			waitFor(t, "downloads", 30*time.Second, func() bool { return allFinished(q) && q.Stats().Total > 0 })
			for _, it := range q.Items() {
				if it.Status != Done {
					t.Fatalf("download item %s: %v %s", it.Remote, it.Status, it.Err)
				}
			}
			for rel, data := range files {
				got, err := os.ReadFile(filepath.Join(dst, filepath.Base(src), filepath.FromSlash(rel)))
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("mismatch for %s (%v)", rel, err)
				}
			}
			if st, err := os.Stat(filepath.Join(dst, filepath.Base(src), "empty")); err != nil || !st.IsDir() {
				t.Fatalf("empty folder not recreated")
			}
		})
	}
}

func acceptAll(string, int, tls.ConnectionState, error) error { return nil }

func TestQueuePauseResumeCancelRetry(t *testing.T) {
	for _, p := range testParams(t) {
		p := p
		if p.Protocol == remote.FTPS {
			p.CertCheck = acceptAll
		}
		t.Run(p.Protocol, func(t *testing.T) {
			q := NewQueue(&Log{})
			q.Max = func() int { return 2 }
			testDelay = 2 * time.Millisecond
			defer func() { testDelay = 0 }()
			dir := t.TempDir()
			big := filepath.Join(dir, "big.bin")
			data := make([]byte, 150<<20)
			rand.Read(data[:1<<20])
			for i := 1; i < 150; i++ {
				copy(data[i<<20:], data[:1<<20])
				data[i<<20] = byte(i)
			}
			os.WriteFile(big, data, 0o644)
			conn, err := remote.Connect(p)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			rdir := path.Join(conn.Home(), "pr-"+strconv.FormatInt(time.Now().UnixNano(), 36))
			conn.Mkdir(rdir)

			q.AddUpload(p, big, rdir)
			it := q.Items()[0]
			waitFor(t, "progress", 20*time.Second, func() bool { return it.Transferred() > 10<<20 })
			q.Pause(it.ID)
			waitFor(t, "paused", 10*time.Second, func() bool { return q.Items()[0].Status == Paused })
			at := it.Transferred()
			if at >= int64(len(data)) {
				t.Skip("transfer finished before pause")
			}
			q.Resume(it.ID)
			waitFor(t, "complete", 60*time.Second, func() bool { s := q.Items()[0].Status; return s == Done || s == Failed })
			if it.Status != Done {
				t.Fatalf("resume failed: %s", it.Err)
			}
			st, err := conn.Stat(path.Join(rdir, "big.bin"))
			if err != nil || st.Size != int64(len(data)) {
				t.Fatalf("remote size %d (%v)", st.Size, err)
			}
			// Download with pause/resume, and verify the bytes.
			local := t.TempDir()
			q.AddDownload(p, remote.Entry{Name: "big.bin", Size: st.Size}, path.Join(rdir, "big.bin"), local)
			d := q.Items()[1]
			waitFor(t, "dl progress", 20*time.Second, func() bool { return d.Transferred() > 10<<20 })
			q.Pause(d.ID)
			waitFor(t, "dl paused", 10*time.Second, func() bool { return d.Status == Paused })
			q.Resume(d.ID)
			waitFor(t, "dl complete", 60*time.Second, func() bool { return d.Status == Done || d.Status == Failed })
			got, _ := os.ReadFile(filepath.Join(local, "big.bin"))
			if !bytes.Equal(got, data) {
				t.Fatalf("downloaded data differs after resume (%d bytes)", len(got))
			}
			// Cancel
			q.AddDownload(p, remote.Entry{Name: "big.bin", Size: st.Size}, path.Join(rdir, "big.bin"), t.TempDir())
			c := q.Items()[2]
			waitFor(t, "c progress", 20*time.Second, func() bool { return c.Transferred() > 1<<20 })
			q.Cancel(c.ID)
			waitFor(t, "cancelled", 10*time.Second, func() bool { return c.Status == Cancelled })
			// Failure + retry: download a file that does not exist yet, then create it.
			q.AddDownload(p, remote.Entry{Name: "late.txt", Size: 5}, path.Join(rdir, "late.txt"), local)
			f := q.Items()[3]
			waitFor(t, "failure", 20*time.Second, func() bool { return f.Status == Failed })
			conn.Upload(path.Join(rdir, "late.txt"), 0, bytes.NewReader([]byte("hello")), nil, nil)
			q.RetryFailed()
			waitFor(t, "retry", 20*time.Second, func() bool {
				return f.Status == Done || f.Status == Failed && f.Err != "" && time.Since(f.Finished) > time.Second
			})
			if f.Status != Done {
				t.Fatalf("retry failed: %s", f.Err)
			}
		})
	}
}
