package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kurofune/claudit/v2/internal/pricing"
)

// noOverlay is a --prices path that does not exist, so the run sees only the
// bundled table whatever the developer's ~/.config/claudit/prices.yaml says.
func noOverlay(t *testing.T) string {
	return filepath.Join(t.TempDir(), "absent.yaml")
}

func runPricesOut(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runPrices(args, &out); err != nil {
		t.Fatalf("runPrices(%q): %v", args, err)
	}
	return out.String()
}

// priceRows parses the table into header fields and model -> row fields.
func priceRows(t *testing.T, out string) ([]string, []string, map[string][]string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("table has %d lines, want header plus rows:\n%s", len(lines), out)
	}
	header := strings.Fields(lines[0])
	var order []string
	rows := map[string][]string{}
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		if len(f) != len(header) {
			t.Fatalf("row %q has %d fields, header has %d", l, len(f), len(header))
		}
		order = append(order, f[0])
		rows[f[0]] = f[1:]
	}
	return header, order, rows
}

func wantModelIDs(tab *pricing.Table) []string {
	var ids []string
	for provider, pp := range tab.Providers {
		for name := range pp.Models {
			ids = append(ids, priceModelID(provider, name))
		}
	}
	sort.Strings(ids)
	return ids
}

func TestPrices_Table(t *testing.T) {
	out := runPricesOut(t, "--prices", noOverlay(t))
	header, order, rows := priceRows(t, out)
	if want := []string{"MODEL", "INPUT", "OUTPUT", "CACHE_READ", "CACHE_W5M", "CACHE_W1H", "PERIODS"}; !reflect.DeepEqual(header, want) {
		t.Errorf("header = %q, want %q", header, want)
	}
	if !sort.StringsAreSorted(order) {
		t.Errorf("rows not sorted by model id: %q", order)
	}
	tab, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if want := wantModelIDs(tab); !reflect.DeepEqual(order, want) {
		t.Errorf("models = %q, want %q", order, want)
	}
	if got, want := rows["claude-opus-5"], []string{"5.00", "25.00", "0.50", "6.25", "10.00", "0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("claude-opus-5 row = %q, want %q", got, want)
	}
	if got := rows["codex/gpt-5.5"]; len(got) == 0 || got[0] != "5.00" {
		t.Errorf("codex/gpt-5.5 row = %q, want input 5.00", got)
	}
	for model, p := range tab.Models {
		if len(p.Rates) > 0 {
			if got, want := rows[model][5], strconv.Itoa(len(p.Rates)); got != want {
				t.Errorf("%s PERIODS = %s, want %s", model, got, want)
			}
		}
	}
}

func TestPrices_JSON(t *testing.T) {
	out := runPricesOut(t, "--json", "--prices", noOverlay(t))
	var got pricing.Table
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output is not a pricing.Table: %v\n%s", err, out)
	}
	want, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wantModelIDs(&got), wantModelIDs(want)) {
		t.Errorf("--json models = %q, want %q", wantModelIDs(&got), wantModelIDs(want))
	}
	if got.Models["claude-opus-5"].Input != 5.00 {
		t.Errorf("--json claude-opus-5 input = %v, want 5", got.Models["claude-opus-5"].Input)
	}
}

func TestPrices_Overlay(t *testing.T) {
	base := runPricesOut(t, "--prices", noOverlay(t))
	_, _, baseRows := priceRows(t, base)

	path := filepath.Join(t.TempDir(), "prices.yaml")
	overlay := `models:
  claude-opus-5:
    input_per_mtok: 7.50
    output_per_mtok: 25.00
    cache_read_per_mtok: 0.50
    cache_write_5m_per_mtok: 6.25
    cache_write_1h_per_mtok: 10.00
`
	if err := os.WriteFile(path, []byte(overlay), 0o600); err != nil {
		t.Fatal(err)
	}
	_, order, rows := priceRows(t, runPricesOut(t, "--prices", path))
	if got, want := rows["claude-opus-5"], []string{"7.50", "25.00", "0.50", "6.25", "10.00", "0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("overridden claude-opus-5 row = %q, want %q", got, want)
	}
	if len(order) != len(baseRows) {
		t.Errorf("overlay changed row count: %d, want %d", len(order), len(baseRows))
	}
	for model, row := range baseRows {
		if model == "claude-opus-5" {
			continue
		}
		if !reflect.DeepEqual(rows[model], row) {
			t.Errorf("%s row = %q, want unchanged %q", model, rows[model], row)
		}
	}
}

func TestPrices_MalformedOverlayErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.yaml")
	if err := os.WriteFile(path, []byte("models: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runPrices([]string{"--prices", path}, &bytes.Buffer{}); err == nil {
		t.Fatal("runPrices with malformed overlay = nil error")
	}
}

// TestPrices_BogusFlagExit2 runs in a child process because flag.ExitOnError
// calls os.Exit, as TestRunServe_RemovedFlagsExit2 does for serve.
func TestPrices_BogusFlagExit2(t *testing.T) {
	if os.Getenv("CLAUDIT_TEST_PRICES_BOGUS") != "" {
		os.Args = []string{"claudit", "prices", "--bogus"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPrices_BogusFlagExit2$")
	cmd.Env = append(os.Environ(), "CLAUDIT_TEST_PRICES_BOGUS=1")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("prices --bogus: err = %v, want exit status 2; stderr:\n%s", err, stderr.String())
	}
	if want := "flag provided but not defined: -bogus"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
	if !strings.Contains(stderr.String(), "Usage of claudit prices") {
		t.Errorf("stderr = %q, want the prices usage", stderr.String())
	}
}

func TestPrices_InHelp(t *testing.T) {
	_, commands, _ := strings.Cut(topLevelUsage, "Commands:\n")
	for _, line := range strings.Split(commands, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "prices" {
			return
		}
	}
	t.Errorf("topLevelUsage Commands has no prices line with a description:\n%s", topLevelUsage)
}

func TestFormatRate(t *testing.T) {
	for in, want := range map[float64]string{0: "0.00", 5: "5.00", 0.5: "0.50", 6.25: "6.25", 0.125: "0.125", 0.03: "0.03"} {
		if got := formatRate(in); got != want {
			t.Errorf("formatRate(%v) = %q, want %q", in, got, want)
		}
	}
}
