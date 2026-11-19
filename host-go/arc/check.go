package arc

import (
	"path"
	"strings"
)

// Safety is what AnySave found when it looked through an archive before unpacking.
type Safety struct {
	Files     int       `json:"files"`
	Dirs      int       `json:"dirs"`
	Total     int64     `json:"total"` // bytes once unpacked (-1 if unknown)
	Ratio     float64   `json:"ratio"` // unpacked size / archive size
	Unsafe    []Skipped `json:"unsafe,omitempty"`
	Links     int       `json:"links"`
	Specials  int       `json:"specials"`
	Junk      int       `json:"junk"`
	Programs  []string  `json:"programs,omitempty"` // first few apps/scripts inside
	LongNames int       `json:"longNames"`
	Block     string    `json:"block,omitempty"` // a reason AnySave won't unpack it
	Warn      []string  `json:"warn,omitempty"`  // things to know
	TopFolder string    `json:"topFolder,omitempty"`
}

var programExt = map[string]bool{".exe": true, ".msi": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true,
	".ps1": true, ".vbs": true, ".js": true, ".jar": true, ".command": true, ".sh": true, ".pkg": true, ".dmg": true,
	".apk": true, ".app": true, ".lnk": true, ".hta": true, ".dll": true}

// Check looks through the list (it reads no data) for anything risky.
func Check(a Archive) *Safety {
	s := &Safety{}
	info := a.Info()
	known := true
	tops := map[string]bool{}
	for _, e := range a.Entries() {
		if e.Dir {
			s.Dirs++
		} else {
			s.Files++
		}
		if e.Size < 0 {
			known = false
		} else {
			s.Total += e.Size
		}
		if e.Name != "" {
			tops[strings.SplitN(e.Name, "/", 2)[0]] = true
		}
		switch {
		case e.Unsafe != "":
			if len(s.Unsafe) < 50 {
				s.Unsafe = append(s.Unsafe, Skipped{e.Name, e.Unsafe})
			}
		case e.Link == "special" || e.Special:
			s.Specials++
		case e.Link != "" && !e.Dir:
			s.Links++
		}
		if IsJunk(e.Name) {
			s.Junk++
		}
		ext := strings.ToLower(path.Ext(e.Name))
		if programExt[ext] && len(s.Programs) < 5 && !e.Dir || (ext == ".app" && e.Dir && !strings.Contains(e.Name, ".app/") && len(s.Programs) < 5) {
			s.Programs = append(s.Programs, e.Name)
		}
		for _, c := range strings.Split(e.Name, "/") {
			if len(c) > 255 {
				s.LongNames++
				break
			}
		}
	}
	if !known {
		s.Total = -1
	}
	if info.Size > 0 && s.Total > 0 {
		s.Ratio = float64(s.Total) / float64(info.Size)
	}
	if len(tops) == 1 {
		for t := range tops {
			s.TopFolder = t
		}
	}
	if info.Warning != "" {
		s.Block = info.Warning
	}
	if s.Total > 1<<30 && s.Ratio > 200 {
		s.Warn = append(s.Warn, "It unpacks to "+Bytes(s.Total)+" from only "+Bytes(info.Size)+". That's unusual (\"zip bomb\" size), so make sure you have the space and trust where it came from.")
	}
	if len(s.Unsafe) > 0 {
		s.Warn = append(s.Warn, count(len(s.Unsafe), "file name tries", "file names try")+" to write outside the folder (\"zip slip\"). AnySave skips them.")
	}
	if s.Links > 0 {
		s.Warn = append(s.Warn, count(s.Links, "link", "links")+" inside. Links aren't unpacked, so they can't point outside the folder.")
	}
	if s.Specials > 0 {
		s.Warn = append(s.Warn, count(s.Specials, "device or special file", "device or special files")+" inside. They're skipped.")
	}
	if len(s.Programs) > 0 {
		s.Warn = append(s.Warn, "It contains programs or scripts. Only open them if you trust where the archive came from.")
	}
	if info.Recovered {
		s.Warn = append(s.Warn, "The archive is damaged or unfinished. AnySave read what it could.")
	}
	return s
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}
