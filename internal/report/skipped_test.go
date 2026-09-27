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

// Only a figure the runner actually measured is labelled so. A dry run, or a
// command whose free space could not be read, reports an estimate.
func TestOnlyMeasuredFiguresAreLabelled(t *testing.T) {
	s := Summary{Selected: []*unit.Unit{
		{ID: "measured", Kind: unit.KindCmd, Measured: true},
		{ID: "estimate", Kind: unit.KindCmd},
		{ID: "paths", Kind: unit.KindPaths},
	}}
	var js bytes.Buffer
	JSON(&js, s)
	if strings.Count(js.String(), `"measured": true`) != 1 {
		t.Errorf("want exactly the measured unit labelled:\n%s", js.String())
	}

	var txt bytes.Buffer
	Text(&txt, s)
	if !strings.Contains(txt.String(), "change in free space") {
		t.Errorf("text report does not say a measured figure is approximate:\n%s", txt.String())
	}
}
