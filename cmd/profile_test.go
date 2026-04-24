package cmd

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"mmcli/internal/config"
)

func TestExtractProfileConfigs(t *testing.T) {
	tmp := t.TempDir()
	paths := config.Paths{
		ConfigDir:   tmp,
		ProfilesDir: filepath.Join(tmp, "profiles"),
	}
	profileName := "p"
	if err := os.MkdirAll(paths.ProfileConfigDir(profileName), 0755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, body string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	// r2modman layout
	write("BepInEx/config/author.mod.cfg", "A")
	write("BepInEx/config/nested/dir/file.json", "B")
	// older / alternate layout
	write("config/legacy.cfg", "C")
	// must be ignored
	write("export.r2x", "profileName: p\nmods: []\n")
	write("BepInEx/plugins/author-mod/mod.dll", "binary")
	write("BepInEx/core/BepInEx.Core.dll", "binary")
	write("changelog.txt", "x")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	extractProfileConfigs(paths, profileName, buf.Bytes())

	configDir := paths.ProfileConfigDir(profileName)
	cases := map[string]string{
		filepath.Join(configDir, "author.mod.cfg"):           "A",
		filepath.Join(configDir, "nested/dir/file.json"):     "B",
		filepath.Join(configDir, "legacy.cfg"):               "C",
	}
	for path, want := range cases {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing extracted file %s: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	for _, unwanted := range []string{
		filepath.Join(configDir, "export.r2x"),
		filepath.Join(configDir, "plugins"),
		filepath.Join(configDir, "core"),
		filepath.Join(configDir, "changelog.txt"),
	} {
		if _, err := os.Stat(unwanted); !os.IsNotExist(err) {
			t.Errorf("unexpected file extracted: %s", unwanted)
		}
	}
}
