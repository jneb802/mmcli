package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mmcli/internal/modpack"
	"mmcli/internal/thunderstore"
)

// Async messages for modpack dep actions.
type modpackUpdateCheckDoneMsg struct {
	updates map[string]string // Owner-Name -> latest version
}
type modpackUpdateDoneMsg struct{ err error }

// Async messages for modpack actions.
type modpackPublishDoneMsg struct{ err error }

type modpackDep struct {
	Owner   string
	Name    string
	Version string
	Raw     string
}

type modpackModel struct {
	manifest     *modpack.Manifest
	deps         []modpackDep
	versionMap   map[string]string // Owner-Name -> Version, for sync view
	depCursor    int
	readmeLines  []string
	readmeScroll int
	iconFile     string   // "icon.png" or ""
	configFiles  []string // files in config/ subdir
	configCursor int
	loadErr      error  // fatal: can't read manifest — blocks all tabs
	statusMsg    string // transient info/error shown on Mods tab only
	editingPath  bool
	pathInput    string

	// Mod actions state
	confirmRemove    bool
	depUpdates       map[string]string // Owner-Name -> latest version
	checkingUpdates  bool
	updatingDep      bool

	// Sync state
	confirmSync bool
	syncDiff    []modpack.SyncDiffItem // preview of what sync will change

	// Publish state
	confirmPublish bool
	publishing     bool
	publishErr     error
	publishDone    bool

	// Settings state
	settingsCursor int    // 0=token, 1=author, 2=path
	editingField   int    // -1=none, 0=token, 1=author, 2=path
	fieldInput     string // current input buffer
}

const modpackReadmeVisible = 30

func (mp *modpackModel) loadFromDisk(modpackPath string) {
	// Reset state
	mp.manifest = nil
	mp.deps = nil
	mp.depCursor = 0
	mp.readmeScroll = 0
	mp.readmeLines = nil
	mp.iconFile = ""
	mp.configFiles = nil
	mp.configCursor = 0
	mp.versionMap = nil
	mp.loadErr = nil
	mp.statusMsg = ""
	mp.editingField = -1
	mp.depUpdates = nil
	mp.checkingUpdates = false

	if modpackPath == "" {
		return
	}

	manifest, err := modpack.LoadManifest(modpackPath)
	if err != nil {
		mp.loadErr = err
		return
	}
	mp.manifest = manifest

	// Parse dependencies
	for _, dep := range manifest.Dependencies {
		ref := thunderstore.ParseDep(dep)
		mp.deps = append(mp.deps, modpackDep{
			Owner:   ref.Owner,
			Name:    ref.Name,
			Version: ref.Version,
			Raw:     dep,
		})
	}

	// Sort deps alphabetically by Owner-Name
	sort.Slice(mp.deps, func(i, j int) bool {
		ni := fmt.Sprintf("%s-%s", mp.deps[i].Owner, mp.deps[i].Name)
		nj := fmt.Sprintf("%s-%s", mp.deps[j].Owner, mp.deps[j].Name)
		return ni < nj
	})

	// Build modpack version map for sync view
	mp.versionMap = make(map[string]string)
	for _, dep := range mp.deps {
		mp.versionMap[fmt.Sprintf("%s-%s", dep.Owner, dep.Name)] = dep.Version
	}

	// Read README.md (optional)
	if readmeData, err := os.ReadFile(filepath.Join(modpackPath, "README.md")); err == nil {
		mp.readmeLines = strings.Split(string(readmeData), "\n")
	}

	// Check icon.png
	if _, err := os.Stat(filepath.Join(modpackPath, "icon.png")); err == nil {
		mp.iconFile = "icon.png"
	}

	// List config files
	configDir := filepath.Join(modpackPath, "config")
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				mp.configFiles = append(mp.configFiles, e.Name())
			}
		}
	}
}
