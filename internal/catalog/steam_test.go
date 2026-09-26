package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func steamapps(t *testing.T, home string) string {
	dir := filepath.Join(home, ".local/share/Steam/steamapps")
	mkdir(t, filepath.Join(dir, "compatdata"))
	return dir
}

func writeManifest(t *testing.T, steamapps, appid, name string) {
	t.Helper()
	body := "\"AppState\"\n{\n\t\"appid\"\t\t\"" + appid + "\"\n\t\"name\"\t\t\"" + name + "\"\n}\n"
	if err := os.WriteFile(filepath.Join(steamapps, "appmanifest_"+appid+".acf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A prefix is a Wine-style filesystem, not a cache: some games keep saves or
// settings there and nowhere else. It must never be reachable without both the
// opt-in flag and --allow-lossy, the same rule docker-volumes follows.
func TestSteamCompatDataIsOptInAndLossy(t *testing.T) {
	home := t.TempDir()
	apps := steamapps(t, home)
	mkdir(t, filepath.Join(apps, "compatdata", "440"))
	writeManifest(t, apps, "440", "Team Fortress 2")

	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	u, ok := r.Get("steam-compatdata")
	if !ok {
		t.Fatal("steam-compatdata not registered though a prefix exists")
	}
	if u.Flag != "--steam-compatdata" {
		t.Errorf("flag = %q, want --steam-compatdata", u.Flag)
	}
	if u.Reversible || u.Tier != unit.TierIrreplaceable {
		t.Errorf("reversible = %v tier = %v, want irreplaceable and irreversible", u.Reversible, u.Tier)
	}
}

// The folder is named by appid alone; the game's own name is what turns
// "delete this" into something a person can actually consent to.
func TestSteamCompatDataNamesTheGameFromItsManifest(t *testing.T) {
	home := t.TempDir()
	apps := steamapps(t, home)
	mkdir(t, filepath.Join(apps, "compatdata", "440"))
	mkdir(t, filepath.Join(apps, "compatdata", "999"))
	writeManifest(t, apps, "440", "Team Fortress 2")
	// 999 has no manifest -- an uninstalled game can still leave its prefix
	// behind, and that must still be reported, just by its bare appid.

	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	u, _ := r.Get("steam-compatdata")
	joined := strings.Join(u.Detail, " ")
	if !strings.Contains(joined, "Team Fortress 2 (440)") {
		t.Errorf("detail %v does not name Team Fortress 2", u.Detail)
	}
	if !strings.Contains(joined, "999") {
		t.Errorf("detail %v drops the unmanifested prefix", u.Detail)
	}
	if len(u.Paths) != 2 {
		t.Errorf("paths %v, want both prefixes", u.Paths)
	}
}

func TestSteamCompatDataAbsentWithoutSteam(t *testing.T) {
	r := Build(Env{Home: t.TempDir(), Has: func(string) bool { return false }})
	if _, ok := r.Get("steam-compatdata"); ok {
		t.Error("registered without a Steam install")
	}
}
