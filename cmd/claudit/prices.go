package main

import (
	"encoding/json"
	"flag"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/kurofune/claudit/internal/pricing"
)

// runPrices prints the rate table serve and watch price turns with: the
// bundled default overlaid by the user prices file. Rates are USD per
// million tokens at each model's current standard rate; PERIODS counts the
// historical rate periods behind it.
func runPrices(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("claudit prices", flag.ExitOnError)
	pricesPath := fs.String("prices", "", "override pricing YAML path (default: ~/.config/claudit/prices.yaml)")
	asJSON := fs.Bool("json", false, "print the loaded table as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tab, err := loadPrices(*pricesPath)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tab)
	}
	return writePriceTable(stdout, tab)
}

type priceRow struct {
	id    string
	price pricing.ModelPrice
}

func writePriceTable(w io.Writer, tab *pricing.Table) error {
	var rows []priceRow
	for provider, pp := range tab.Providers {
		for name, p := range pp.Models {
			rows = append(rows, priceRow{priceModelID(provider, name), p})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	ew := &errWriter{w: tw}
	ew.Print("MODEL\tINPUT\tOUTPUT\tCACHE_READ\tCACHE_W5M\tCACHE_W1H\tPERIODS\n")
	for _, r := range rows {
		ew.Print(strings.Join([]string{
			r.id,
			formatRate(r.price.Input),
			formatRate(r.price.Output),
			formatRate(r.price.CacheRead),
			formatRate(r.price.CacheWrite5m),
			formatRate(r.price.CacheWrite1h),
			strconv.Itoa(len(r.price.Rates)),
		}, "\t") + "\n")
	}
	if ew.err != nil {
		return ew.err
	}
	return tw.Flush()
}

// priceModelID names a model in the table: Claude Code models by their bare
// id, other providers' models as provider/model.
func priceModelID(provider, model string) string {
	if provider == pricing.ClaudeCode {
		return model
	}
	return provider + "/" + model
}

// formatRate prints a rate with at least two decimals and as many more as
// it needs, so 0.125 is not rounded to 0.13.
func formatRate(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	whole, frac, _ := strings.Cut(s, ".")
	for len(frac) < 2 {
		frac += "0"
	}
	return whole + "." + frac
}
