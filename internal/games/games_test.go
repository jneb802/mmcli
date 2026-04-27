package games

import (
	"runtime"
	"testing"
)

func TestGetReturnsRegisteredGame(t *testing.T) {
	g, err := Get("valheim")
	if err != nil {
		t.Fatalf("Get(valheim) failed: %v", err)
	}
	if g.ID != "valheim" {
		t.Errorf("expected ID=valheim, got %q", g.ID)
	}
	if g.LoaderPack.Owner != "denikson" || g.LoaderPack.Name != "BepInExPack_Valheim" {
		t.Errorf("expected denikson/BepInExPack_Valheim, got %s/%s", g.LoaderPack.Owner, g.LoaderPack.Name)
	}
}

func TestGetUnknownGameReturnsError(t *testing.T) {
	_, err := Get("not-a-game")
	if err == nil {
		t.Fatal("expected error for unknown game")
	}
}

func TestExecutableForCoversAllPlatforms(t *testing.T) {
	g := MustGet("valheim")
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if g.ExecutableFor(goos) == "" {
			t.Errorf("missing executable for valheim on %s", goos)
		}
	}
}

func TestExecutableForUnsupportedOSReturnsEmpty(t *testing.T) {
	g := MustGet("valheim")
	if got := g.ExecutableFor("plan9"); got != "" {
		t.Errorf("expected empty for plan9, got %q", got)
	}
}

func TestSupportedOnCurrentRuntime(t *testing.T) {
	g := MustGet("valheim")
	if !g.SupportedOn(runtime.GOOS) {
		t.Skipf("valheim not supported on %s — fine, but ensure tests run on a supported OS", runtime.GOOS)
	}
}

func TestAllReturnsRegistryStable(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("expected at least one registered game")
	}
	first := All()
	for i := range first {
		if first[i].ID != all[i].ID {
			t.Errorf("All() order not stable: index %d differs", i)
		}
	}
}

func TestValheimCapabilities(t *testing.T) {
	g := MustGet("valheim")
	if !g.Capabilities.SupportsAgent {
		t.Error("expected valheim to support agent flow")
	}
	if !g.Capabilities.SupportsAnticheat {
		t.Error("expected valheim to support anticheat")
	}
}

func TestLoaderPackFullName(t *testing.T) {
	p := LoaderPack{Owner: "denikson", Name: "BepInExPack_Valheim"}
	if got := p.FullName(); got != "denikson/BepInExPack_Valheim" {
		t.Errorf("expected denikson/BepInExPack_Valheim, got %q", got)
	}
}
