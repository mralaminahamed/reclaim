package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestCompletionPrintsAScriptForEachShell(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		out, code := run(t, t.TempDir(), "completion", sh)
		if code != 0 {
			t.Errorf("completion %s exited %d:\n%s", sh, code, out)
		}
		if !strings.Contains(out, "reclaim") {
			t.Errorf("completion %s produced nothing usable:\n%s", sh, out)
		}
	}
}

func TestCompletionRejectsAnUnknownShell(t *testing.T) {
	out, code := run(t, t.TempDir(), "completion", "csh")

	if code == 0 {
		t.Fatalf("an unknown shell was accepted:\n%s", out)
	}
	if !strings.Contains(out, "bash") {
		t.Errorf("the error does not say what is supported:\n%s", out)
	}
}

func TestCompletionWithoutAShellIsAnError(t *testing.T) {
	if _, code := run(t, t.TempDir(), "completion"); code == 0 {
		t.Fatal("completion with no argument succeeded")
	}
}

// Unit ids are the arguments nobody can be expected to remember, and they
// change with what is installed. The scripts ask the binary rather than
// carrying a list that would go stale.
func TestUnitsListsIdsOnePerLine(t *testing.T) {
	home := fixtureHome(t)
	out, code := run(t, home, "__units")

	if code != 0 {
		t.Fatalf("__units exited %d:\n%s", code, out)
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		t.Fatal("no unit ids")
	}
	for _, want := range []string{"pip-cache", "npm-cacache"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from:\n%s", want, out)
		}
	}
	if strings.Contains(out, " ") && !strings.Contains(out, "\n") {
		t.Error("ids are not one per line")
	}
}

// The hidden command is a completion helper, not part of the interface.
func TestUnitsIsNotAdvertised(t *testing.T) {
	out, _ := run(t, t.TempDir(), "--help")

	if strings.Contains(out, "__units") {
		t.Errorf("a helper is listed as a command:\n%s", out)
	}
	if !strings.Contains(out, "completion") {
		t.Errorf("completion is not listed as a command:\n%s", out)
	}
}

// subcommandsWithFlags are checked both ways: every flag a subcommand defines
// must be offered by every script, and every flag a script offers must exist.
// The one-way check let "analyze -n" ship -- completion offered a flag the
// binary rejects.
var subcommandsWithFlags = []string{"clean", "status", "analyze", "history", "index"}

// definedFlags parses "<sub> --help", which is the FlagSet's own listing.
func definedFlags(t *testing.T, sub string) map[string]bool {
	t.Helper()
	help, _ := run(t, t.TempDir(), sub, "--help")
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s+-([a-zA-Z0-9-]+)`).FindAllStringSubmatch(help, -1) {
		out[spell(m[1])] = true
	}
	return out
}

// spell is the conventional spelling: one dash for a single letter, two
// otherwise. Go accepts either; completion offers this one.
func spell(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// section returns the text of a script that belongs to one subcommand.
func section(sh, script, sub string) string {
	switch sh {
	case "bash", "zsh":
		start := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(sub) + `\)`).FindStringIndex(script)
		if start == nil {
			return ""
		}
		rest := script[start[1]:]
		end := strings.Index(rest, ";;")
		if end < 0 {
			// A layout this parser does not know returns nothing, so every
			// defined flag is reported missing: loud, never a silent pass.
			return ""
		}
		body := rest[:end]
		if sh == "zsh" && strings.Contains(body, "$clean_flags") {
			i := strings.Index(script, "clean_flags=(")
			if i < 0 {
				return ""
			}
			j := strings.Index(script[i:], "\n    )")
			if j < 0 {
				return ""
			}
			// The array and anything written inline beside it.
			return script[i:i+j] + "\n" + body
		}
		return body
	default: // fish: one complete line per flag, scoped by subcommand
		var b strings.Builder
		for _, line := range strings.Split(script, "\n") {
			if strings.Contains(line, "__fish_seen_subcommand_from "+sub+"'") {
				b.WriteString(line + "\n")
			}
		}
		return b.String()
	}
}

func offeredFlags(sh, text string) map[string]bool {
	out := map[string]bool{}
	if sh == "fish" {
		for _, m := range regexp.MustCompile(`\s-l ([a-z0-9-]+)`).FindAllStringSubmatch(text, -1) {
			out["--"+m[1]] = true
		}
		for _, m := range regexp.MustCompile(`\s-s ([a-zA-Z0-9])`).FindAllStringSubmatch(text, -1) {
			out["-"+m[1]] = true
		}
		return out
	}
	if sh == "bash" {
		// Only the word list handed to compgen is offered; compgen's own -W
		// is not a reclaim flag.
		var words []string
		for _, m := range regexp.MustCompile(`-W "([^"]*)"`).FindAllStringSubmatch(text, -1) {
			words = append(words, m[1])
		}
		text = strings.Join(words, " ")
	}
	for _, m := range regexp.MustCompile(`(?:^|[\s"'(])(--?[a-zA-Z0-9][a-zA-Z0-9-]*)`).FindAllStringSubmatch(text, -1) {
		out[m[1]] = true
	}
	return out
}

func TestCompletionAndFlagsAgreeBothWays(t *testing.T) {
	scripts := map[string]string{}
	for _, sh := range []string{"bash", "zsh", "fish"} {
		scripts[sh], _ = run(t, t.TempDir(), "completion", sh)
	}
	total := 0
	for _, sub := range subcommandsWithFlags {
		defined := definedFlags(t, sub)
		total += len(defined)
		for sh, script := range scripts {
			offered := offeredFlags(sh, section(sh, script, sub))
			for f := range defined {
				if !offered[f] {
					t.Errorf("%s completion does not offer %s %s", sh, sub, f)
				}
			}
			for f := range offered {
				if !defined[f] {
					t.Errorf("%s completion offers %s %s, which does not exist", sh, sub, f)
				}
			}
		}
	}
	// Guard the parser itself: a regexp that matches nothing passes vacuously.
	if total < 40 {
		t.Fatalf("found only %d defined flags across subcommands; parsing is broken", total)
	}
}

// A flag that takes a value must be handled in bash's "$prev" case, or tab
// after it offers more flags where a value belongs. index --root shipped that
// way.
func TestBashKnowsEveryValueTakingFlag(t *testing.T) {
	script, _ := run(t, t.TempDir(), "completion", "bash")
	start := strings.Index(script, `case "$prev" in`)
	end := strings.Index(script[start:], "esac")
	if start < 0 || end < 0 {
		t.Fatal("bash script has no $prev case; parsing is broken")
	}
	handled := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s+([-a-z0-9|]+)\)`).FindAllStringSubmatch(script[start:start+end], -1) {
		for _, f := range strings.Split(m[1], "|") {
			handled[f] = true
		}
	}
	valued := regexp.MustCompile(`(?m)^\s+-([a-zA-Z0-9-]+) \w+`)
	n := 0
	for _, sub := range subcommandsWithFlags {
		help, _ := run(t, t.TempDir(), sub, "--help")
		for _, m := range valued.FindAllStringSubmatch(help, -1) {
			n++
			if f := spell(m[1]); !handled[f] {
				t.Errorf("bash completion offers flags after %s %s, which takes a value", sub, f)
			}
		}
	}
	if n < 10 {
		t.Fatalf("found only %d value-taking flags; parsing is broken", n)
	}
}
