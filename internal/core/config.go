// Package core holds ShoreDrop's platform-independent application state:
// settings, saved sites, credentials, the transfer queue and the log.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	AppName    = "ShoreDrop"
	AppVersion = "1.0.0"
)

// WindowState remembers the main window layout.
type WindowState struct {
	X, Y, W, H  int
	Maximized   bool
	PaneSplit   float64 // fraction of width given to the local pane
	QueueHeight int     // in 96-DPI pixels
}

// Settings are the user's preferences.
type Settings struct {
	Theme           string `json:"theme"` // "light", "dark" or "system"
	DefaultProtocol string `json:"defaultProtocol"`
	TimeoutSec      int    `json:"timeoutSec"`
	MaxTransfers    int    `json:"maxTransfers"`
	FTPMode         string `json:"ftpMode"`     // "passive" or "active"
	DownloadDir     string `json:"downloadDir"` // default local folder
	UploadDir       string `json:"uploadDir"`   // default remote folder
	ResumeTransfers bool   `json:"resumeTransfers"`
	SyncByDefault   bool   `json:"syncByDefault"`
	ShowHidden      bool   `json:"showHidden"`
	ConfirmDelete   bool   `json:"confirmDelete"`
	WarnPlainFTP    bool   `json:"warnPlainFtp"`
	ExistsAction    string `json:"existsAction"` // "overwrite", "resume", "skip"
	KeepAlive       bool   `json:"keepAlive"`

	Window       WindowState `json:"window"`
	PlainFTPOK   []string    `json:"plainFtpAcknowledged"` // hosts the user said not to warn about
	LastHost     string      `json:"lastHost"`
	LastUser     string      `json:"lastUser"`
	LastPort     int         `json:"lastPort"`
	LastProtocol string      `json:"lastProtocol"`
	LastLocalDir string      `json:"lastLocalDir"`
}

// DefaultSettings returns factory settings.
func DefaultSettings() Settings {
	home, _ := os.UserHomeDir()
	dl := filepath.Join(home, "Downloads")
	if _, err := os.Stat(dl); err != nil {
		dl = home
	}
	return Settings{
		Theme:           "system",
		DefaultProtocol: "sftp",
		TimeoutSec:      30,
		MaxTransfers:    4,
		FTPMode:         "passive",
		DownloadDir:     dl,
		UploadDir:       "",
		ResumeTransfers: true,
		SyncByDefault:   false,
		ShowHidden:      false,
		ConfirmDelete:   true,
		WarnPlainFTP:    true,
		ExistsAction:    "overwrite",
		KeepAlive:       true,
		Window:          WindowState{W: 1320, H: 900, PaneSplit: 0.5, QueueHeight: 250},
	}
}

// ConfigDir returns the per-user configuration directory.
func ConfigDir() string {
	if d := os.Getenv("SHOREDROP_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, AppName)
}

var fileMu sync.Mutex

func writeJSON(name string, v any) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

func readJSON(name string, v any) error {
	data, err := os.ReadFile(filepath.Join(ConfigDir(), name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// LoadSettings reads settings, falling back to defaults for missing values.
func LoadSettings() Settings {
	s := DefaultSettings()
	readJSON("settings.json", &s)
	if s.MaxTransfers < 1 || s.MaxTransfers > 10 {
		s.MaxTransfers = 4
	}
	if s.TimeoutSec < 5 || s.TimeoutSec > 600 {
		s.TimeoutSec = 30
	}
	switch s.Theme {
	case "light", "dark", "system":
	default:
		s.Theme = "system"
	}
	if s.FTPMode != "active" {
		s.FTPMode = "passive"
	}
	switch s.ExistsAction {
	case "overwrite", "resume", "skip":
	default:
		s.ExistsAction = "overwrite"
	}
	if s.Window.PaneSplit < 0.2 || s.Window.PaneSplit > 0.8 {
		s.Window.PaneSplit = 0.5
	}
	if s.Window.QueueHeight < 120 {
		s.Window.QueueHeight = 250
	}
	return s
}

// Save writes settings to disk.
func (s *Settings) Save() error { return writeJSON("settings.json", s) }

// Timeout returns the connection timeout.
func (s *Settings) Timeout() time.Duration { return time.Duration(s.TimeoutSec) * time.Second }

// PlainFTPAcknowledged reports whether the user opted out of the plain FTP warning for host.
func (s *Settings) PlainFTPAcknowledged(host string) bool {
	for _, h := range s.PlainFTPOK {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// ---------------- trust stores ----------------

// TrustStore remembers SSH host keys and accepted FTPS certificates.
type TrustStore struct {
	HostKeys map[string]string `json:"hostKeys"` // "host:port" -> "type base64(sha256)"
	Certs    map[string]string `json:"certs"`    // "host:port" -> sha256 hex of leaf certificate
}

func LoadTrust() *TrustStore {
	t := &TrustStore{}
	readJSON("trust.json", t)
	if t.HostKeys == nil {
		t.HostKeys = map[string]string{}
	}
	if t.Certs == nil {
		t.Certs = map[string]string{}
	}
	return t
}

func (t *TrustStore) Save() error { return writeJSON("trust.json", t) }

// SortedKeys is a helper for stable output.
func SortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
