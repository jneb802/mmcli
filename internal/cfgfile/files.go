package cfgfile

import (
	"os"
	"path/filepath"
	"strings"
)

// ConfigExtensions are recognized config file extensions for syncing.
var ConfigExtensions = map[string]bool{
	".cfg":  true,
	".yaml": true,
	".yml":  true,
	".json": true,
}

// ExcludedNames are files/dirs to skip entirely.
var ExcludedNames = map[string]bool{
	".DS_Store": true,
}

// ExcludedExtensions are extensions that are not config files.
var ExcludedExtensions = map[string]bool{
	".blueprint": true,
	".valreplay": true,
	".bak":       true,
	".zip":       true,
	".txt":       true,
}

// ListConfigFiles returns config files (relative paths) in a directory, recursing into subdirs.
// Only includes files with recognized config extensions.
func ListConfigFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		name := info.Name()

		// Skip excluded names
		if ExcludedNames[name] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(name))

		// Skip excluded extensions
		if ExcludedExtensions[ext] {
			return nil
		}

		// Only include recognized config extensions
		if !ConfigExtensions[ext] {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}

		files = append(files, rel)
		return nil
	})

	return files, err
}

// IsCfg returns true if the file is a BepInEx .cfg file (entry-level diffable).
func IsCfg(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".cfg"
}
