package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/apps"
)

func twoApps() []apps.App {
	return []apps.App{
		{Name: "Alpha", Bytes: 2 << 20, Steps: []apps.Step{{Argv: []string{"apt", "remove", "alpha"}, Root: true}}},
		{Name: "Beta", Bytes: 1 << 20, Dir: "/opt/Beta", Steps: []apps.Step{
			{Argv: []string{"rm", "-rf", "/opt/Beta"}, Root: true},
			{Argv: []string{"rm", "/home/u/.local/share/applications/Beta.desktop"}},
		}},
	}
}

type recorder struct {
	ran     []string
	fail    string
	deleted []string
}

func (r *recorder) rmAll(p string) error {
	r.deleted = append(r.deleted, p)
	return nil
}

func (r *recorder) run(s apps.Step) error {
	c := strings.Join(s.Argv, " ")
	r.ran = append(r.ran, c)
	if r.fail != "" && strings.Contains(c, r.fail) {
		return errors.New("boom")
	}
	return nil
}

func drive(in string, list []apps.App, r *recorder) (string, int) {
	var out bytes.Buffer
	env := apps.Env{DirBytes: func(string) int64 { return 0 }}
	code := removeApps(strings.NewReader(in), &out, list, env, r.run, r.rmAll)
	return out.String(), code
}

// Listing comes first and answering nothing removes nothing.
func TestRemoveAppsListsThenAbortsOnNone(t *testing.T) {
	for _, in := range []string{"n\n", "\n", ""} {
		r := &recorder{}
		out, code := drive(in, twoApps(), r)
		if len(r.ran) != 0 || code != 0 {
			t.Errorf("%q ran %v code %d", in, r.ran, code)
		}
		if !strings.Contains(out, "Alpha") || !strings.Contains(out, "Beta") {
			t.Errorf("list missing:\n%s", out)
		}
	}
}

// Picking is by number, and the exact commands are shown before the final yes.
func TestRemoveAppsRunsOnlyPickedAfterConfirm(t *testing.T) {
	r := &recorder{}
	out, code := drive("2\ny\n", twoApps(), r)
	want := []string{"rm -rf /opt/Beta", "rm /home/u/.local/share/applications/Beta.desktop"}
	if code != 0 || strings.Join(r.ran, "|") != strings.Join(want, "|") {
		t.Errorf("ran %v code %d", r.ran, code)
	}
	if !strings.Contains(out, "sudo rm -rf /opt/Beta") {
		t.Errorf("exact command not shown before confirming:\n%s", out)
	}
}

func TestRemoveAppsDeclinedConfirmRunsNothing(t *testing.T) {
	r := &recorder{}
	if _, code := drive("1,2\nno\n", twoApps(), r); len(r.ran) != 0 || code != 0 {
		t.Errorf("ran %v code %d", r.ran, code)
	}
}

// A typo must not be read as a smaller or different selection.
func TestRemoveAppsRejectsBadSelection(t *testing.T) {
	for _, in := range []string{"3\n", "0\n", "1,x\n", "-1\n", "all\n"} {
		r := &recorder{}
		if _, code := drive(in+"y\n", twoApps(), r); len(r.ran) != 0 || code != 2 {
			t.Errorf("%q ran %v code %d", in, r.ran, code)
		}
	}
}

// Deleting the launcher after a failed directory removal would leave an app
// that is half there and no longer listed.
func TestRemoveAppsStopsAnAppAtItsFirstFailure(t *testing.T) {
	r := &recorder{fail: "rm -rf"}
	out, code := drive("1,2\ny\n", twoApps(), r)
	for _, c := range r.ran {
		if strings.HasSuffix(c, "Beta.desktop") {
			t.Errorf("launcher removed after failed step: %v", r.ran)
		}
	}
	if code != 1 || !strings.Contains(out, "Beta") || !strings.Contains(out, "failed") {
		t.Errorf("code %d\n%s", code, out)
	}
}

// The step list is built from /proc and disk; a surprising one is refused
// rather than run.
func TestRemoveAppsRefusesUnsafeSteps(t *testing.T) {
	bad := [][]apps.Step{
		{{Argv: []string{"rm", "-rf", "/"}, Root: true}},
		{{Argv: []string{"rm", "-rf", "/opt"}, Root: true}},
		{{Argv: []string{"rm", "-rf", "/usr/local/bin"}, Root: true}},
		{{Argv: []string{"rm", "-rf", "/opt/x/../.."}, Root: true}},
		{{Argv: []string{"rm", "/home/u/file.txt"}}},
		{{Argv: []string{"sh", "-c", "echo hi"}}},
		{{Argv: []string{"apt", "remove", "-y"}, Root: true}},
		{},
	}
	for _, steps := range bad {
		r := &recorder{}
		list := []apps.App{{Name: "Bad", Steps: steps}}
		if _, code := drive("1\ny\n", list, r); len(r.ran) != 0 || code == 0 {
			t.Errorf("%+v ran %v code %d", steps, r.ran, code)
		}
	}
}

// Whatever the app kept is shown after removal and left alone.
func TestRemoveAppsReportsLeftoversWithoutRemovingThem(t *testing.T) {
	home := t.TempDir()
	if err := mkdirAll(home + "/.config/Beta"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := &recorder{}
	env := apps.Env{Home: home, DirBytes: func(string) int64 { return 4096 }}
	removeApps(strings.NewReader("2\ny\n"), &out, twoApps(), env, r.run, r.rmAll)
	if !strings.Contains(out.String(), home+"/.config/Beta") {
		t.Errorf("leftover not reported:\n%s", out.String())
	}
	for _, c := range r.ran {
		if strings.Contains(c, ".config") {
			t.Errorf("leftover touched: %v", r.ran)
		}
	}
	if len(r.deleted) != 0 {
		t.Errorf("leftover deleted without being asked: %v", r.deleted)
	}
}

func leftoverHome(t *testing.T) (string, apps.Env) {
	home := t.TempDir()
	for _, d := range []string{"/.config/Beta", "/.cache/beta"} {
		if err := mkdirAll(home + d); err != nil {
			t.Fatal(err)
		}
	}
	return home, apps.Env{Home: home, DirBytes: func(string) int64 { return 10 }}
}

// Leftovers are a second question, asked per app, and yes deletes exactly the
// paths that were listed.
func TestRemoveAppsDeletesLeftoversOnSeparateYes(t *testing.T) {
	home, env := leftoverHome(t)
	var out bytes.Buffer
	r := &recorder{}
	code := removeApps(strings.NewReader("2\ny\ny\n"), &out, twoApps(), env, r.run, r.rmAll)
	want := home + "/.cache/beta|" + home + "/.config/Beta"
	if code != 0 || strings.Join(r.deleted, "|") != want {
		t.Errorf("code %d deleted %v\n%s", code, r.deleted, out.String())
	}
}

func TestRemoveAppsKeepsLeftoversOnNo(t *testing.T) {
	_, env := leftoverHome(t)
	var out bytes.Buffer
	r := &recorder{}
	removeApps(strings.NewReader("2\ny\nn\n"), &out, twoApps(), env, r.run, r.rmAll)
	if len(r.deleted) != 0 {
		t.Errorf("deleted %v", r.deleted)
	}
}

// An app that failed to uninstall is still installed and still using its data.
func TestRemoveAppsDoesNotOfferLeftoversAfterFailure(t *testing.T) {
	_, env := leftoverHome(t)
	var out bytes.Buffer
	r := &recorder{fail: "rm -rf"}
	removeApps(strings.NewReader("2\ny\ny\n"), &out, twoApps(), env, r.run, r.rmAll)
	if len(r.deleted) != 0 || strings.Contains(out.String(), "Delete") {
		t.Errorf("deleted %v\n%s", r.deleted, out.String())
	}
}

func TestSafeLeftover(t *testing.T) {
	h := "/home/u"
	ok := []string{h + "/.config/Beta", h + "/.postman", h + "/snap/x", h + "/.var/app/org.x"}
	bad := []string{h, h + "/", "/", "/etc/x", h + "/.config", h + "/.config/", h + "/.cache",
		h + "/.local/share", h + "/Documents/../..", h + "/.config/../../etc", "home/u/.x", h + "/.var/app", h + "/snap"}
	for _, p := range ok {
		if !safeLeftover(h, p) {
			t.Errorf("%s refused", p)
		}
	}
	for _, p := range bad {
		if safeLeftover(h, p) {
			t.Errorf("%s allowed", p)
		}
	}
}

func mkdirAll(p string) error { return os.MkdirAll(p, 0o755) }
