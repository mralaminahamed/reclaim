package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/apps"
	"github.com/mralaminahamed/reclaim/internal/fsutil"
)

var pkgName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9+._-]*$`)

// checkStep admits only the removal commands the apps package builds. Steps
// come from files on disk, so a surprising one is refused instead of run.
func checkStep(s apps.Step) error {
	a := s.Argv
	switch {
	case len(a) == 3 && (a[0] == "apt" && a[1] == "remove" || a[0] == "snap" && a[1] == "remove" ||
		a[0] == "flatpak" && a[1] == "uninstall"):
		if !pkgName.MatchString(a[2]) {
			return fmt.Errorf("odd package name %q", a[2])
		}
	case len(a) == 3 && a[0] == "rm" && a[1] == "-rf":
		if filepath.Clean(a[2]) != a[2] || apps.OwnedDir(a[2]+"/x") != a[2] {
			return fmt.Errorf("%s is not an application directory", a[2])
		}
	case len(a) == 2 && a[0] == "rm":
		if !filepath.IsAbs(a[1]) || filepath.Clean(a[1]) != a[1] || !strings.HasSuffix(a[1], ".desktop") {
			return fmt.Errorf("%s is not a launcher", a[1])
		}
	default:
		return fmt.Errorf("unexpected command %q", strings.Join(a, " "))
	}
	return nil
}

// parsePicks reads "1,3" into zero-based indexes. Anything it cannot read
// whole is an error: a typo must not become a smaller, different selection.
func parsePicks(line string, n int) ([]int, bool) {
	line = strings.TrimSpace(line)
	var out []int
	seen := map[int]bool{}
	for _, f := range strings.Split(line, ",") {
		i, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || i < 1 || i > n {
			return nil, false
		}
		if !seen[i] {
			seen[i] = true
			out = append(out, i-1)
		}
	}
	return out, true
}

// safeLeftover admits only a path strictly inside home that is not one of the
// shared directories apps keep data in: deleting ~/.config would take every
// app's settings with it.
func safeLeftover(home, p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || filepath.Clean(home) != home {
		return false
	}
	rel, ok := strings.CutPrefix(p, home+"/")
	if !ok || rel == "" {
		return false
	}
	switch rel {
	case ".config", ".cache", ".local", ".local/share", ".var", ".var/app", "snap":
		return false
	}
	return true
}

func removeApps(in io.Reader, out io.Writer, list []apps.App, env apps.Env,
	run func(apps.Step) error, rmAll func(string) error) int {
	if len(list) == 0 {
		fmt.Fprintln(out, "nothing to remove")
		return 0
	}
	fmt.Fprintln(out, "\n== Remove which? ==")
	for i, a := range list {
		fmt.Fprintf(out, "  %d) %-28s %10s  last used %s\n", i+1, a.Name,
			fsutil.Human(a.Bytes), a.LastUsed.Format("2006-01-02"))
		fmt.Fprintf(out, "       %s\n", renderSteps(a.Steps))
	}
	r := bufio.NewReader(in)
	fmt.Fprint(out, "Numbers to remove, e.g. 1,3 (anything else cancels): ")
	line, _ := r.ReadString('\n')
	if t := strings.TrimSpace(strings.ToLower(line)); t == "" || t == "n" || t == "no" {
		fmt.Fprintln(out, "nothing removed")
		return 0
	}
	picks, ok := parsePicks(line, len(list))
	if !ok {
		fmt.Fprintf(out, "not a list of numbers 1-%d, nothing removed\n", len(list))
		return 2
	}

	for _, i := range picks {
		if len(list[i].Steps) == 0 {
			fmt.Fprintf(out, "%s has no removal command, nothing removed\n", list[i].Name)
			return 1
		}
		for _, s := range list[i].Steps {
			if err := checkStep(s); err != nil {
				fmt.Fprintf(out, "refusing %s: %v, nothing removed\n", list[i].Name, err)
				return 1
			}
		}
	}

	fmt.Fprintln(out, "\nAbout to run:")
	var total int64
	for _, i := range picks {
		total += list[i].Bytes
		for _, s := range list[i].Steps {
			fmt.Fprintf(out, "  %s\n", renderSteps([]apps.Step{s}))
		}
	}
	fmt.Fprintf(out, "Removes %d app(s), about %s, and cannot be undone. Continue? [y/N] ",
		len(picks), fsutil.Human(total))
	ans, _ := r.ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
		fmt.Fprintln(out, "nothing removed")
		return 0
	}

	code := 0
	for _, i := range picks {
		a := list[i]
		failed := false
		for _, s := range a.Steps {
			if err := run(s); err != nil {
				fmt.Fprintf(out, "%s: failed at %q: %v\n", a.Name, strings.Join(s.Argv, " "), err)
				failed, code = true, 1
				break
			}
		}
		if failed {
			continue
		}
		fmt.Fprintf(out, "removed %s\n", a.Name)
		if c := deleteLeftovers(r, out, env, a, rmAll); c != 0 {
			code = c
		}
	}
	return code
}

// deleteLeftovers lists what an uninstalled app kept in the home directory and
// deletes it only on a separate yes. The default is to keep it.
func deleteLeftovers(r *bufio.Reader, out io.Writer, env apps.Env, a apps.App,
	rmAll func(string) error) int {
	var safe []apps.Leftover
	var total int64
	for _, l := range apps.Leftovers(env, a) {
		if safeLeftover(env.Home, l.Path) {
			safe = append(safe, l)
			total += l.Bytes
		}
	}
	if len(safe) == 0 {
		return 0
	}
	fmt.Fprintf(out, "\n%s left in your home:\n", a.Name)
	for _, l := range safe {
		fmt.Fprintf(out, "  %-60s %10s\n", l.Path, fsutil.Human(l.Bytes))
	}
	fmt.Fprintf(out, "Delete these %d (%s)? Settings and data cannot be recovered. [y/N] ",
		len(safe), fsutil.Human(total))
	ans, _ := r.ReadString('\n')
	if t := strings.ToLower(strings.TrimSpace(ans)); t != "y" && t != "yes" {
		fmt.Fprintln(out, "kept")
		return 0
	}
	code := 0
	for _, l := range safe {
		if err := rmAll(l.Path); err != nil {
			fmt.Fprintf(out, "failed to delete %s: %v\n", l.Path, err)
			code = 1
		}
	}
	return code
}

func renderSteps(steps []apps.Step) string {
	var parts []string
	for _, s := range steps {
		c := strings.Join(s.Argv, " ")
		if s.Root {
			c = "sudo " + c
		}
		parts = append(parts, c)
	}
	return strings.Join(parts, " && ")
}

// runStep runs one removal command with the terminal attached, so apt can show
// what else it would remove and ask. Privilege comes from sudo unless already root.
func runStep(s apps.Step) error {
	argv := s.Argv
	if s.Root && os.Geteuid() != 0 {
		argv = append([]string{"sudo"}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
