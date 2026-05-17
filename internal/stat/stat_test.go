package stat

import "testing"

func TestMedian(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"empty", nil, 0},
		{"one", []float64{4}, 4},
		{"odd", []float64{3, 1, 2}, 2},
		{"even", []float64{4, 1, 2, 3}, 2.5},
		{"sorted-noop", []float64{1, 2, 3, 4, 5}, 3},
		{"negatives", []float64{-2, -1, 0, 1, 2}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Median(append([]float64(nil), c.in...))
			if got != c.want {
				t.Errorf("Median(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
