package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"shoredrop/internal/remote"
)

// Logon types for a site.
const (
	LogonNormal    = "normal"    // username + saved password
	LogonAsk       = "ask"       // ask for the password when connecting
	LogonAnonymous = "anonymous" // FTP anonymous login
	LogonKey       = "key"       // SFTP private key file
)

// Site is a saved connection. Passwords are never stored here; they live in
// the operating system's credential store under CredentialTarget().
type Site struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Folder       string `json:"folder"` // "" for top level, "Work/Clients" for nested
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Logon        string `json:"logon"`
	User         string `json:"user"`
	KeyFile      string `json:"keyFile,omitempty"`
	RemoteDir    string `json:"remoteDir,omitempty"`
	LocalDir     string `json:"localDir,omitempty"`
	TransferMode string `json:"transferMode,omitempty"` // "default", "passive", "active"
	Notes        string `json:"notes,omitempty"`
}

// CredentialTarget is the credential-store key for this site's password.
func (s *Site) CredentialTarget() string { return "ShoreDrop/site/" + s.ID }

// Label is "Name — PROTO".
func (s *Site) Label() string { return s.Name + " — " + remote.ProtocolLabel(s.Protocol) }

// SiteStore holds all saved sites and folders.
type SiteStore struct {
	Folders []string `json:"folders"`
	Sites   []Site   `json:"sites"`
}

// NewID returns a random identifier.
func NewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func LoadSites() *SiteStore {
	s := &SiteStore{}
	readJSON("sites.json", s)
	s.normalize()
	return s
}

func (s *SiteStore) Save() error {
	s.normalize()
	return writeJSON("sites.json", s)
}

func (s *SiteStore) normalize() {
	seen := map[string]bool{}
	var folders []string
	add := func(f string) {
		f = CleanFolder(f)
		for f != "" && !seen[f] {
			seen[f] = true
			folders = append(folders, f)
			i := strings.LastIndex(f, "/")
			if i < 0 {
				break
			}
			f = f[:i]
		}
	}
	for _, f := range s.Folders {
		add(f)
	}
	for i := range s.Sites {
		s.Sites[i].Folder = CleanFolder(s.Sites[i].Folder)
		add(s.Sites[i].Folder)
		if s.Sites[i].ID == "" {
			s.Sites[i].ID = NewID()
		}
	}
	sort.Slice(folders, func(i, j int) bool { return strings.ToLower(folders[i]) < strings.ToLower(folders[j]) })
	s.Folders = folders
	sort.SliceStable(s.Sites, func(i, j int) bool {
		return strings.ToLower(s.Sites[i].Name) < strings.ToLower(s.Sites[j].Name)
	})
}

// CleanFolder normalises a folder path.
func CleanFolder(f string) string {
	parts := strings.FieldsFunc(f, func(r rune) bool { return r == '/' || r == '\\' })
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

func (s *SiteStore) Find(id string) *Site {
	for i := range s.Sites {
		if s.Sites[i].ID == id {
			return &s.Sites[i]
		}
	}
	return nil
}

// Upsert adds or replaces a site.
func (s *SiteStore) Upsert(site Site) {
	if site.ID == "" {
		site.ID = NewID()
	}
	for i := range s.Sites {
		if s.Sites[i].ID == site.ID {
			s.Sites[i] = site
			s.normalize()
			return
		}
	}
	s.Sites = append(s.Sites, site)
	s.normalize()
}

// Delete removes a site and its stored password.
func (s *SiteStore) Delete(id string) {
	for i := range s.Sites {
		if s.Sites[i].ID == id {
			Credentials.Delete(s.Sites[i].CredentialTarget())
			s.Sites = append(s.Sites[:i], s.Sites[i+1:]...)
			return
		}
	}
}

// AddFolder creates a folder.
func (s *SiteStore) AddFolder(f string) error {
	f = CleanFolder(f)
	if f == "" {
		return errors.New("folder name is empty")
	}
	s.Folders = append(s.Folders, f)
	s.normalize()
	return nil
}

// RenameFolder renames a folder (and all sub-folders/sites inside it).
func (s *SiteStore) RenameFolder(old, newName string) {
	old = CleanFolder(old)
	parent := ""
	if i := strings.LastIndex(old, "/"); i >= 0 {
		parent = old[:i] + "/"
	}
	nw := CleanFolder(parent + newName)
	re := func(f string) string {
		if f == old {
			return nw
		}
		if strings.HasPrefix(f, old+"/") {
			return nw + f[len(old):]
		}
		return f
	}
	for i := range s.Folders {
		s.Folders[i] = re(s.Folders[i])
	}
	for i := range s.Sites {
		s.Sites[i].Folder = re(s.Sites[i].Folder)
	}
	s.normalize()
}

// DeleteFolder removes a folder and everything inside it.
func (s *SiteStore) DeleteFolder(f string) {
	f = CleanFolder(f)
	in := func(x string) bool { return x == f || strings.HasPrefix(x, f+"/") }
	var folders []string
	for _, x := range s.Folders {
		if !in(x) {
			folders = append(folders, x)
		}
	}
	s.Folders = folders
	var sites []Site
	for _, x := range s.Sites {
		if in(x.Folder) {
			Credentials.Delete(x.CredentialTarget())
			continue
		}
		sites = append(sites, x)
	}
	s.Sites = sites
	s.normalize()
}

// ForgetAllPasswords removes every stored password.
func (s *SiteStore) ForgetAllPasswords() {
	for i := range s.Sites {
		Credentials.Delete(s.Sites[i].CredentialTarget())
	}
}
