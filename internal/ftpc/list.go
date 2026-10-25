package ftpc

import (
	"strconv"
	"strings"
	"time"
)

func parseMLSD(line string) (Entry, bool) {
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return Entry{}, false
	}
	facts, name := line[:sp], line[sp+1:]
	e := Entry{Name: name}
	for _, f := range strings.Split(facts, ";") {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "type":
			lv := strings.ToLower(v)
			switch {
			case lv == "dir":
				e.IsDir = true
			case lv == "cdir" || lv == "pdir":
				return Entry{}, false
			case strings.HasPrefix(lv, "os.unix=slink") || strings.HasPrefix(lv, "os.unix=symlink"):
				e.IsLink = true
			}
		case "size", "sizd":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && strings.ToLower(k) == "size" {
				e.Size = n
			}
		case "modify":
			if len(v) >= 14 {
				if t, err := time.ParseInLocation("20060102150405", v[:14], time.UTC); err == nil {
					e.ModTime = t.Local()
				}
			}
		case "unix.mode":
			if m, err := strconv.ParseUint(v, 8, 32); err == nil {
				e.Perm = uint32(m) & 0o7777
				e.HasPerm = true
			}
		case "perm":
			if !e.HasPerm {
				e.PermStr = v
			}
		}
	}
	if e.HasPerm {
		e.PermStr = PermString(e.Perm, e.IsDir, e.IsLink)
	}
	return e, true
}

var months = map[string]time.Month{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

type token struct {
	s     string
	start int
	end   int
}

func tokenize(line string) []token {
	var out []token
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		j := i
		for j < len(line) && line[j] != ' ' && line[j] != '\t' {
			j++
		}
		out = append(out, token{line[i:j], i, j})
		i = j
	}
	return out
}

func parseLIST(line string, now time.Time) (Entry, bool) {
	if strings.HasPrefix(line, "total ") {
		return Entry{}, false
	}
	toks := tokenize(line)
	if len(toks) == 0 {
		return Entry{}, false
	}
	if len(toks[0].s) >= 2 && toks[0].s[0] >= '0' && toks[0].s[0] <= '9' && strings.ContainsAny(toks[0].s, "-/") {
		return parseDOS(line, toks)
	}
	if len(toks) < 6 {
		return Entry{}, false
	}
	perm := toks[0].s
	e := Entry{}
	switch perm[0] {
	case 'd':
		e.IsDir = true
	case 'l':
		e.IsLink = true
	}
	// Find the month token: size precedes it, day and time/year follow.
	for i := 2; i+2 < len(toks); i++ {
		mon, ok := months[strings.ToLower(toks[i].s)]
		if !ok {
			continue
		}
		day, err := strconv.Atoi(toks[i+1].s)
		if err != nil || day < 1 || day > 31 {
			continue
		}
		size, err := strconv.ParseInt(toks[i-1].s, 10, 64)
		if err != nil {
			continue
		}
		ty := toks[i+2].s
		var t time.Time
		if strings.Contains(ty, ":") {
			hm := strings.SplitN(ty, ":", 2)
			h, _ := strconv.Atoi(hm[0])
			m, _ := strconv.Atoi(hm[1])
			t = time.Date(now.Year(), mon, day, h, m, 0, 0, time.Local)
			if t.After(now.Add(48 * time.Hour)) {
				t = t.AddDate(-1, 0, 0)
			}
		} else {
			y, err := strconv.Atoi(ty)
			if err != nil {
				continue
			}
			t = time.Date(y, mon, day, 0, 0, 0, 0, time.Local)
		}
		if i+3 >= len(toks) {
			return Entry{}, false
		}
		name := line[toks[i+3].start:]
		if e.IsLink {
			if k := strings.Index(name, " -> "); k >= 0 {
				name = name[:k]
			}
		}
		e.Name = name
		e.Size = size
		e.ModTime = t
		if p, ok := ParsePermString(perm); ok {
			e.Perm = p
			e.HasPerm = true
		}
		e.PermStr = perm
		return e, true
	}
	return Entry{}, false
}

func parseDOS(line string, toks []token) (Entry, bool) {
	if len(toks) < 4 {
		return Entry{}, false
	}
	var t time.Time
	for _, layout := range []string{"01-02-06 03:04PM", "01-02-2006 03:04PM", "01-02-06 15:04", "2006-01-02 15:04", "01/02/2006 03:04 PM"} {
		if tt, err := time.ParseInLocation(layout, toks[0].s+" "+toks[1].s, time.Local); err == nil {
			t = tt
			break
		}
	}
	e := Entry{ModTime: t}
	if strings.EqualFold(toks[2].s, "<DIR>") {
		e.IsDir = true
	} else {
		n, err := strconv.ParseInt(strings.ReplaceAll(toks[2].s, ",", ""), 10, 64)
		if err != nil {
			return Entry{}, false
		}
		e.Size = n
	}
	e.Name = line[toks[3].start:]
	return e, true
}

// ParsePermString converts "drwxr-xr-x" to permission bits.
func ParsePermString(s string) (uint32, bool) {
	if len(s) < 10 {
		return 0, false
	}
	var m uint32
	bits := s[1:10]
	for i, ch := range bits {
		bit := uint32(1) << uint(8-i)
		switch ch {
		case 'r', 'w', 'x':
			m |= bit
		case 's':
			m |= bit
			if i == 2 {
				m |= 0o4000
			} else if i == 5 {
				m |= 0o2000
			}
		case 'S':
			if i == 2 {
				m |= 0o4000
			} else if i == 5 {
				m |= 0o2000
			}
		case 't':
			m |= bit | 0o1000
		case 'T':
			m |= 0o1000
		case '-':
		default:
			return 0, false
		}
	}
	return m, true
}

// PermString renders permission bits like ls -l.
func PermString(m uint32, dir, link bool) string {
	b := []byte("----------")
	switch {
	case link:
		b[0] = 'l'
	case dir:
		b[0] = 'd'
	}
	const rwx = "rwxrwxrwx"
	for i := 0; i < 9; i++ {
		if m&(1<<uint(8-i)) != 0 {
			b[i+1] = rwx[i]
		}
	}
	if m&0o4000 != 0 {
		if b[3] == 'x' {
			b[3] = 's'
		} else {
			b[3] = 'S'
		}
	}
	if m&0o2000 != 0 {
		if b[6] == 'x' {
			b[6] = 's'
		} else {
			b[6] = 'S'
		}
	}
	if m&0o1000 != 0 {
		if b[9] == 'x' {
			b[9] = 't'
		} else {
			b[9] = 'T'
		}
	}
	return string(b)
}
