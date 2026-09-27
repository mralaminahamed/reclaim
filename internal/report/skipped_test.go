package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/remove"
	"github.com/mralaminahamed/reclaim/internal/unit"
)

func TestSkippedIsReportedInTextAndJSON(t *testing.T) {
	s := Summary{Skipped: []remove.Skip{{Path: "/home/u/.cache/uv/x", Reason: "changed during removal"}}}

	var txt bytes.Buffer
	Text(&txt, s)
	if !strings.Contains(txt.String(), "== Skipped ==") || !strings.Contains(txt.String(), "changed during removal") {
		t.Errorf("text report lacks the skip:\n%s", txt.String())
	}

	var js bytes.Buffer
	if err := JSON(&js, s); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.Unmarshal(js.Bytes(), &out)
	if sk, _ := out["skipped"].([]any); len(sk) != 1 {
		t.Errorf("JSON skipped = %v, want one entry", out["skipped"])
	}
}

func TestCommandUnitsAreMarkedMeasured(t *testing.T) {
	s := Summary{Selected: []*unit.Unit{{ID: "c", Kind: unit.KindCmd}, {ID: "p", Kind: unit.KindPaths}}}
	var js bytes.Buffer
	JSON(&js, s)
	if !strings.Contains(js.String(), `"measured": true`) || strings.Count(js.String(), `"measured": true`) != 1 {
		t.Errorf("want exactly the command unit marked measured:\n%s", js.String())
	}
}
