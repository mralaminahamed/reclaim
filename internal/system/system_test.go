package system

import (
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func TestUnitsRequireTheTool(t *testing.T) {
	r := unit.NewRegistry()
	Add(r, Env{Has: func(bin string) bool { return bin == "apt-get" }})

	if _, ok := r.Get("system-apt"); !ok {
		t.Error("apt unit missing though apt-get is installed")
	}
	if _, ok := r.Get("system-snaps"); ok {
		t.Error("snap unit registered though snap is absent")
	}
}

func TestAllSystemUnitsAreOptIn(t *testing.T) {
	// These need root and touch shared system state, so none may run just
	// because the tier ceiling allows it. Most carry the blanket --system
	// flag; kernels and Time Machine carry their own more specific one for
	// the same reason a byte count is not enough to consent to either -- but
	// every unit here must be behind some named flag.
	r := unit.NewRegistry()
	Add(r, Env{
		Has: func(string) bool { return true },
		KernelPackages: func() []string {
			return []string{"linux-image-6.8.0-45-generic", "linux-image-6.8.0-52-generic"}
		},
		RunningKernel: func() string { return "6.8.0-52-generic" },
		CrashDirs:     []string{t.TempDir()},
	})
	if len(r.All()) == 0 {
		t.Fatal("no system units registered")
	}
	for _, u := range r.All() {
		if u.Flag == "" {
			t.Errorf("unit %q has no opt-in flag", u.ID)
		}
		if u.Kind != unit.KindCmd {
			t.Errorf("unit %q should be a command unit", u.ID)
		}
		// system-crash hints /var, where crash dumps actually live; every
		// other unit here reclaims from the root filesystem.
		want := "/"
		if u.ID == "system-crash" {
			want = "/var"
		}
		if u.MountHint != want {
			t.Errorf("unit %q hints mount %q, want %q", u.ID, u.MountHint, want)
		}
	}
}

func TestSystemUnitsDeclareThatTheyNeedRoot(t *testing.T) {
	// Without this the runner cannot ask for a password up front, and each unit
	// fails separately with an unexplained non-zero exit.
	r := unit.NewRegistry()
	Add(r, Env{
		Has: func(string) bool { return true },
		KernelPackages: func() []string {
			return []string{"linux-image-6.8.0-45-generic", "linux-image-6.8.0-52-generic"}
		},
		RunningKernel: func() string { return "6.8.0-52-generic" },
		CrashDirs:     []string{t.TempDir()},
	})
	for _, u := range r.All() {
		if !u.NeedsRoot {
			t.Errorf("unit %q does not declare NeedsRoot", u.ID)
		}
	}
}

func TestSnapRemovalPropagatesFailure(t *testing.T) {
	// A while loop at the end of a pipeline exits 0 even when every command
	// inside it failed, so snap removal used to fail completely silently.
	r := unit.NewRegistry()
	Add(r, Env{Has: func(string) bool { return true }})
	u, ok := r.Get("system-snaps")
	if !ok {
		t.Fatal("snap unit missing")
	}
	if !contains(u.Command, "|| exit") {
		t.Errorf("snap command swallows failures: %q", u.Command)
	}
}

func TestJournalVacuumIsBounded(t *testing.T) {
	// An unbounded vacuum would delete the entire journal. It must keep a
	// window of history.
	r := unit.NewRegistry()
	Add(r, Env{Has: func(string) bool { return true }, JournalKeep: "200M"})
	u, ok := r.Get("system-journal")
	if !ok {
		t.Fatal("journal unit missing")
	}
	if u.Command == "" || !contains(u.Command, "200M") {
		t.Errorf("journal command %q does not honour the keep size", u.Command)
	}
}

func TestTimeMachineThinningIsLossyAndMeasuredAfterTheFact(t *testing.T) {
	// A local snapshot is the only copy of the state it captured, and nothing
	// on disk says in advance how many bytes thinning will actually take.
	r := unit.NewRegistry()
	Add(r, Env{Has: func(bin string) bool { return bin == "tmutil" },
		LocalSnapshots: func() []string { return []string{"com.apple.TimeMachine.2026-09-01.local"} }})

	u, ok := r.Get("system-tm-thin")
	if !ok {
		t.Fatal("system-tm-thin missing though tmutil is present")
	}
	if u.Reversible || u.Tier != unit.TierIrreplaceable {
		t.Errorf("reversible = %v tier = %v, want irreplaceable and irreversible", u.Reversible, u.Tier)
	}
	if u.Flag != "--timemachine" {
		t.Errorf("flag = %q, want --timemachine", u.Flag)
	}
	if !u.MeasureFreed {
		t.Error("want MeasureFreed: nothing on disk states thinning's yield in advance")
	}
	if len(u.Detail) != 1 || !contains(u.Detail[0], "1 local snapshot") {
		t.Errorf("detail %v does not name the current snapshot count", u.Detail)
	}
}

func TestTimeMachineAbsentWithoutTmutil(t *testing.T) {
	r := unit.NewRegistry()
	Add(r, Env{Has: func(string) bool { return false }})
	if _, ok := r.Get("system-tm-thin"); ok {
		t.Error("registered without tmutil")
	}
	if _, ok := r.Get("system-unified-log"); ok {
		t.Error("registered without tmutil")
	}
}

func TestUnifiedLogEraseOnlyTakesExpiredEntries(t *testing.T) {
	// --ttl (not --all) is what keeps this reversible: it takes only what
	// macOS itself already marked expired, never recent diagnostics.
	r := unit.NewRegistry()
	Add(r, Env{Has: func(string) bool { return true }})

	u, ok := r.Get("system-unified-log")
	if !ok {
		t.Fatal("system-unified-log missing though tmutil and log are present")
	}
	if !u.Reversible {
		t.Error("want reversible: --ttl only takes what is already expired")
	}
	if contains(u.Command, "--all") {
		t.Errorf("command %q would erase live diagnostics", u.Command)
	}
	if !contains(u.Command, "--ttl") {
		t.Errorf("command %q does not use --ttl", u.Command)
	}
}

func TestAptUnitDoesNotAutoremoveWithoutConsent(t *testing.T) {
	// autoremove can pull out kernels and packages the user still wants, so the
	// default must be the cache clean only.
	r := unit.NewRegistry()
	Add(r, Env{Has: func(string) bool { return true }})
	u, _ := r.Get("system-apt")
	if contains(u.Command, "autoremove") {
		t.Errorf("default apt command runs autoremove: %q", u.Command)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
