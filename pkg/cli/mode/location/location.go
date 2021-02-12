// Package location implements an addon that supports viewing location history
// and changing to a selected directory.
package location

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"src.elv.sh/pkg/cli"
	"src.elv.sh/pkg/cli/mode"
	"src.elv.sh/pkg/cli/tk"
	"src.elv.sh/pkg/fsutil"
	"src.elv.sh/pkg/store"
	"src.elv.sh/pkg/ui"
)

// Config is the configuration to start the location history feature.
type Config struct {
	// Binding is the key binding.
	Binding tk.Handler
	// Store provides the directory history and the function to change directory.
	Store Store
	// IteratePinned specifies pinned directories by calling the given function
	// with all pinned directories.
	IteratePinned func(func(string))
	// IterateHidden specifies hidden directories by calling the given function
	// with all hidden directories.
	IterateHidden func(func(string))
	// IterateWorksapce specifies workspace configuration.
	IterateWorkspaces WorkspaceIterator
}

// Store defines the interface for interacting with the directory history.
type Store interface {
	Dirs(blacklist map[string]struct{}) ([]store.Dir, error)
	Chdir(dir string) error
	Getwd() (string, error)
}

// A special score for pinned directories.
var pinnedScore = math.Inf(1)

// Start starts the directory history feature.
func Start(app cli.App, cfg Config) {
	if cfg.Store == nil {
		app.Notify("no dir history store")
		return
	}

	dirs := []store.Dir{}
	blacklist := map[string]struct{}{}
	wsKind, wsRoot := "", ""

	if cfg.IteratePinned != nil {
		cfg.IteratePinned(func(s string) {
			blacklist[s] = struct{}{}
			dirs = append(dirs, store.Dir{Score: pinnedScore, Path: s})
		})
	}
	if cfg.IterateHidden != nil {
		cfg.IterateHidden(func(s string) { blacklist[s] = struct{}{} })
	}
	wd, err := cfg.Store.Getwd()
	if err == nil {
		blacklist[wd] = struct{}{}
		if cfg.IterateWorkspaces != nil {
			wsKind, wsRoot = cfg.IterateWorkspaces.Parse(wd)
		}
	}
	storedDirs, err := cfg.Store.Dirs(blacklist)
	if err != nil {
		app.Notify("db error: " + err.Error())
		if len(dirs) == 0 {
			return
		}
	}
	for _, dir := range storedDirs {
		if filepath.IsAbs(dir.Path) {
			dirs = append(dirs, dir)
		} else if wsKind != "" && hasPathPrefix(dir.Path, wsKind) {
			dirs = append(dirs, dir)
		}
	}

	l := list{dirs}

	w := tk.NewComboBox(tk.ComboBoxSpec{
		CodeArea: tk.CodeAreaSpec{
			Prompt: mode.Prompt(" LOCATION ", true),
		},
		ListBox: tk.ListBoxSpec{
			OverlayHandler: cfg.Binding,
			OnAccept: func(it tk.Items, i int) {
				path := it.(list).dirs[i].Path
				if strings.HasPrefix(path, wsKind) {
					path = wsRoot + path[len(wsKind):]
				}
				err := cfg.Store.Chdir(path)
				if err != nil {
					app.Notify(err.Error())
				}
				app.SetAddon(nil, false)
			},
		},
		OnFilter: func(w tk.ComboBox, p string) {
			w.ListBox().Reset(l.filter(p), 0)
		},
	})
	app.SetAddon(w, false)
	app.Redraw()
}

func hasPathPrefix(path, prefix string) bool {
	return path == prefix ||
		strings.HasPrefix(path, prefix+string(filepath.Separator))
}

// WorkspaceIterator is a function that iterates all workspaces by calling
// the passed function with the name and pattern of each kind of workspace.
// Iteration should stop when the called function returns false.
type WorkspaceIterator func(func(kind, pattern string) bool)

// Parse returns whether the path matches any kind of workspace. If there is
// a match, it returns the kind of the workspace and the root. It there is no
// match, it returns "", "".
func (ws WorkspaceIterator) Parse(path string) (kind, root string) {
	var foundKind, foundRoot string
	ws(func(kind, pattern string) bool {
		if !strings.HasPrefix(pattern, "^") {
			pattern = "^" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			// TODO(xiaq): Surface the error.
			return true
		}
		if root := re.FindString(path); root != "" {
			foundKind, foundRoot = kind, root
			return false
		}
		return true
	})
	return foundKind, foundRoot
}

type list struct {
	dirs []store.Dir
}

func (l list) filter(p string) list {
	if p == "" {
		return l
	}
	re := makeRegexpForPattern(p)
	var filteredDirs []store.Dir
	for _, dir := range l.dirs {
		if re.MatchString(fsutil.TildeAbbr(dir.Path)) {
			filteredDirs = append(filteredDirs, dir)
		}
	}
	return list{filteredDirs}
}

var (
	quotedPathSep = regexp.QuoteMeta(string(os.PathSeparator))
	emptyRe       = regexp.MustCompile("")
)

func makeRegexpForPattern(p string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i).*") // Ignore case, unanchored
	for i, seg := range strings.Split(p, string(os.PathSeparator)) {
		if i > 0 {
			b.WriteString(".*" + quotedPathSep + ".*")
		}
		b.WriteString(regexp.QuoteMeta(seg))
	}
	b.WriteString(".*")
	re, err := regexp.Compile(b.String())
	if err != nil {
		// TODO: Log the error.
		return emptyRe
	}
	return re
}

func (l list) Show(i int) ui.Text {
	return ui.T(fmt.Sprintf("%s %s",
		showScore(l.dirs[i].Score), fsutil.TildeAbbr(l.dirs[i].Path)))
}

func (l list) Len() int { return len(l.dirs) }

func showScore(f float64) string {
	if f == pinnedScore {
		return "  *"
	}
	return fmt.Sprintf("%3.0f", f)
}
