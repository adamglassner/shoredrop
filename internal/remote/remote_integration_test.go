package remote

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"testing"
	"time"

	"shoredrop/internal/sshc"
)

// Integration tests run against servers started by the test harness
// (testsrv/sshd.py and testsrv/ftpd.py). They are skipped otherwise.

func exercise(t *testing.T, fs FS, size int) {
	t.Helper()
	base := fs.Home()
	dir := path.Join(base, fmt.Sprintf("sd-test-%d", time.Now().UnixNano()))
	if err := fs.Mkdir(dir); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := make([]byte, size)
	rand.Read(data)
	f := path.Join(dir, "upload me.bin")
	var up int
	if err := fs.Upload(f, 0, bytes.NewReader(data), nil, func(n int) { up += n }); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if up != size {
		t.Fatalf("upload progress %d != %d", up, size)
	}
	list, err := fs.List(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "upload me.bin" || list[0].Size != int64(size) || list[0].IsDir {
		t.Fatalf("unexpected listing: %+v", list)
	}
	st, err := fs.Stat(f)
	if err != nil || st.Size != int64(size) {
		t.Fatalf("stat: %+v %v", st, err)
	}
	var got bytes.Buffer
	if err := fs.Download(f, 0, &got, nil, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("download mismatch: %d bytes vs %d", got.Len(), size)
	}
	// resume download from the middle
	got.Reset()
	half := int64(size / 2)
	if err := fs.Download(f, half, &got, nil, nil); err != nil {
		t.Fatalf("resume download: %v", err)
	}
	if !bytes.Equal(got.Bytes(), data[half:]) {
		t.Fatalf("resumed download mismatch")
	}
	// resume upload: write first half, then the rest from offset
	f2 := path.Join(dir, "resumed.bin")
	if err := fs.Upload(f2, 0, bytes.NewReader(data[:half]), nil, nil); err != nil {
		t.Fatalf("upload part1: %v", err)
	}
	if err := fs.Upload(f2, half, bytes.NewReader(data[half:]), nil, nil); err != nil {
		t.Fatalf("upload part2: %v", err)
	}
	got.Reset()
	if err := fs.Download(f2, 0, &got, nil, nil); err != nil || !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("resumed upload mismatch (%v)", err)
	}
	f3 := path.Join(dir, "renamed.bin")
	if err := fs.Rename(f, f3); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := fs.Chmod(f3, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if st, err := fs.Stat(f3); err != nil {
		t.Fatalf("stat after rename: %v", err)
	} else if st.HasPerm && st.Perm&0o777 != 0o640 {
		t.Fatalf("chmod not applied: %o", st.Perm)
	}
	list, _ = fs.List(dir)
	for _, e := range list {
		if e.Name == "renamed.bin" && e.HasPerm && e.Perm&0o777 != 0o640 {
			t.Fatalf("list perm %o", e.Perm)
		}
	}
	if _, err := fs.Stat(path.Join(dir, "nope")); err == nil {
		t.Fatalf("stat of missing file succeeded")
	}
	for _, n := range []string{"renamed.bin", "resumed.bin"} {
		if err := fs.Remove(path.Join(dir, n)); err != nil {
			t.Fatalf("remove: %v", err)
		}
	}
	if err := fs.Rmdir(dir); err != nil {
		t.Fatalf("rmdir: %v", err)
	}
}

func sshPort(t *testing.T, env string) int {
	v := os.Getenv(env)
	if v == "" {
		t.Skip(env + " not set")
	}
	p, _ := strconv.Atoi(v)
	return p
}

func TestSFTPPassword(t *testing.T) {
	port := sshPort(t, "SD_SSH_PORT")
	var seen string
	fs, err := Connect(Params{Protocol: SFTP, Host: "127.0.0.1", Port: port, User: "tester", Password: "s3cret pass",
		HostKey: func(h string, p int, kt string, blob []byte) error {
			seen = kt + " " + sshc.Fingerprint(blob)
			return nil
		},
		Log: func(d, l string) { t.Log(d, l) }})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	t.Log("host key:", seen, fs.Description())
	exercise(t, fs, 3*1024*1024+123)
}

func TestSFTPWrongPassword(t *testing.T) {
	port := sshPort(t, "SD_SSH_PORT")
	_, err := Connect(Params{Protocol: SFTP, Host: "127.0.0.1", Port: port, User: "tester", Password: "nope"})
	if err == nil {
		t.Fatal("expected auth failure")
	}
	t.Log(err)
}

func TestSFTPKeys(t *testing.T) {
	port := sshPort(t, "SD_SSH_PORT")
	dir := os.Getenv("SD_KEYDIR")
	for _, k := range []string{"client_ed25519", "client_ecdsa", "client_rsa_pem", "client_rsa_openssh"} {
		t.Run(k, func(t *testing.T) {
			fs, err := Connect(Params{Protocol: SFTP, Host: "127.0.0.1", Port: port, User: "tester", KeyFile: path.Join(dir, k)})
			if err != nil {
				t.Fatal(err)
			}
			defer fs.Close()
			if _, err := fs.List(fs.Home()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSFTPRejectHostKey(t *testing.T) {
	port := sshPort(t, "SD_SSH_PORT")
	_, err := Connect(Params{Protocol: SFTP, Host: "127.0.0.1", Port: port, User: "tester", Password: "s3cret pass",
		HostKey: func(h string, p int, kt string, blob []byte) error { return fmt.Errorf("rejected") }})
	if err == nil {
		t.Fatal("expected rejection")
	}
}

func ftpPort(t *testing.T) int {
	v := os.Getenv("SD_FTP_PORT")
	if v == "" {
		t.Skip("SD_FTP_PORT not set")
	}
	p, _ := strconv.Atoi(v)
	return p
}

func TestFTPModes(t *testing.T) {
	port := ftpPort(t)
	iport, _ := strconv.Atoi(os.Getenv("SD_FTPS_IMPLICIT_PORT"))
	cases := []struct {
		name    string
		proto   string
		port    int
		passive bool
	}{
		{"plain-passive", FTP, port, true},
		{"plain-active", FTP, port, false},
		{"explicit-tls-passive", FTPS, port, true},
		{"explicit-tls-active", FTPS, port, false},
		{"implicit-tls", FTPSImplicit, iport, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.port == 0 {
				t.Skip("no port")
			}
			prompted := false
			fs, err := Connect(Params{Protocol: c.proto, Host: "127.0.0.1", Port: c.port, User: "tester", Password: "s3cret pass", Passive: c.passive,
				Timeout: 10 * time.Second,
				CertCheck: func(h string, p int, cs tls.ConnectionState, verr error) error {
					prompted = true
					return nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			defer fs.Close()
			if c.proto != FTP && !prompted {
				t.Fatal("self-signed certificate was not offered for confirmation")
			}
			t.Log(fs.Description())
			exercise(t, fs, 2*1024*1024+77)
		})
	}
}

var _ = io.EOF
