package aggregate

import (
	"sort"
	"time"

	"github.com/kurofune/claudit/internal/stat"
)

// AnomalyKind enumerates the kinds of statistical outliers the report
// flags. Cost spikes and hit-ratio drops are computed independently from
// the same per-bucket trend series.
type AnomalyKind string

const (
	AnomalyCostSpike    AnomalyKind = "cost_spike"
	AnomalyHitRatioDrop AnomalyKind = "hitratio_drop"
)

// Anomaly is a single flagged trend bucket.
//
// For cost spikes: Value is the bucket's cost in USD, Baseline is the
// rolling median of the prior eligible buckets' costs, and Ratio is
// Value / Baseline (always >= the cost threshold).
//
// For hit-ratio drops: Value is the bucket's hit ratio (0..1), Baseline
// is the rolling median of prior eligible buckets' hit ratios, and
// Ratio is the absolute percentage-point gap (Baseline − Value),
// expressed as a fraction in 0..1 so renderers can format consistently.
type Anomaly struct {
	Time     time.Time   `json:"time"`
	Kind     AnomalyKind `json:"kind"`
	Period   Period      `json:"period"`
	Value    float64     `json:"value"`
	Baseline float64     `json:"baseline"`
	Ratio    float64     `json:"ratio"`
}

// Thresholds. Kept as unexported constants — the values were chosen to
// match the roadmap's "spike >2x median" / "hit ratio drop" intuition
// and to avoid flooding the report with noise on day-to-day variance.
const (
	anomalyWindow         = 7   // rolling-median lookback, in trend buckets
	anomalyCostMultiplier = 2.0 // cost flagged when value/median >= this
	anomalyHitRatioDrop   = 0.2 // hit-ratio flagged when median−value >= this
)

// Anomalies returns flagged trend buckets in chronological order. Empty
// when trend mode is off or fewer than anomalyWindow+1 buckets exist (we
// need at least one trailing window plus the bucket under test).
//
// The detector operates on whatever bucket size the report was built
// with (--by=day|week|month). The roadmap describes "7-day rolling
// median for daily cost"; the same shape generalizes — 7 weekly or 7
// monthly buckets is still a meaningful local baseline.
func (a *Aggregator) Anomalies() []Anomaly {
	if !a.period.Valid() {
		return nil
	}
	pts := a.TrendTotals()
	if len(pts) <= anomalyWindow {
		return nil
	}

	var out []Anomaly
	for i := anomalyWindow; i < len(pts); i++ {
		// Cost spike — needs a non-zero median to avoid divide-by-zero
		// and to prevent every $0.01 day after a long idle stretch from
		// looking 100x suspicious.
		costs := make([]float64, 0, anomalyWindow)
		for j := i - anomalyWindow; j < i; j++ {
			costs = append(costs, pts[j].CostUSD)
		}
		medCost := stat.Median(costs)
		v := pts[i].CostUSD
		if medCost > 0 && v/medCost >= anomalyCostMultiplier {
			out = append(out, Anomaly{
				Time:     pts[i].Time,
				Kind:     AnomalyCostSpike,
				Period:   a.period,
				Value:    v,
				Baseline: medCost,
				Ratio:    v / medCost,
			})
		}

		// Hit-ratio drop — only consider buckets with cacheable tokens
		// on both sides. A bucket with no cache-eligible traffic has no
		// meaningful hit ratio, and including its synthetic 0 would
		// pull the median down and flag everything afterwards.
		if pts[i].Tokens.CacheableTokens() == 0 {
			continue
		}
		ratios := make([]float64, 0, anomalyWindow)
		for j := i - anomalyWindow; j < i; j++ {
			if pts[j].Tokens.CacheableTokens() == 0 {
				continue
			}
			ratios = append(ratios, pts[j].Tokens.HitRatio())
		}
		if len(ratios) < anomalyWindow/2 {
			// Not enough prior cache-eligible buckets to baseline against.
			continue
		}
		medRatio := stat.Median(ratios)
		hr := pts[i].Tokens.HitRatio()
		if medRatio-hr >= anomalyHitRatioDrop {
			out = append(out, Anomaly{
				Time:     pts[i].Time,
				Kind:     AnomalyHitRatioDrop,
				Period:   a.period,
				Value:    hr,
				Baseline: medRatio,
				Ratio:    medRatio - hr,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Time.Equal(out[j].Time) {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Time.Before(out[j].Time)
	})
	return out
}

