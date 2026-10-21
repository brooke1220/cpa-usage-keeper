package ranking

import (
	"reflect"
	"testing"
)

func TestMissingLatencyDoesNotChangeMeasuredComparisonPopulation(t *testing.T) {
	measured := localMeasuredUtilities([]float64{100, 200, 300}, []int64{20, 20, 20}, false)
	withMissing := localMeasuredUtilities([]float64{0, 100, 200, 300}, []int64{0, 20, 20, 20}, false)
	if withMissing[0] != 0.5 || !reflect.DeepEqual(measured, withMissing[1:]) {
		t.Fatalf("missing duration distorted measured rankings: measured=%v withMissing=%v", measured, withMissing)
	}
}

func TestFullyMeasuredUtilitiesKeepOriginalScoring(t *testing.T) {
	for _, higherIsBetter := range []bool{false, true} {
		values := []float64{10, 20, 100, 500}
		samples := []int64{1, 20, 100, 1000}
		original := localShrinkUtilitiesToNeutral(localRobustUtilities(values, higherIsBetter), samples)
		if actual := localMeasuredUtilities(values, samples, higherIsBetter); !reflect.DeepEqual(original, actual) {
			t.Fatalf("complete-data scoring changed: want=%v got=%v", original, actual)
		}
	}
}
