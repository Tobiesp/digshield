package digshield

import (
	"math"
	"testing"
)

func TestFitCalibration(t *testing.T) {
	calibration, err := FitCalibration("reference-v1", "model-v1", []Sample{
		{TotalCost: 1, ElapsedMilliseconds: 2},
		{TotalCost: 2, ElapsedMilliseconds: 4},
		{TotalCost: 3, ElapsedMilliseconds: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calibration.millisecondsPerCost != 2 || calibration.trainingRMSE != 0 ||
		calibration.minCost != 1 || calibration.maxCost != 3 || calibration.samples != 3 {
		t.Fatalf("unexpected calibration: %+v", calibration)
	}
}

func TestFitCalibrationReportsResidualError(t *testing.T) {
	calibration, err := FitCalibration("ref", "v1", []Sample{{1, 1}, {2, 5}, {3, 5}})
	if err != nil {
		t.Fatal(err)
	}
	if calibration.trainingRMSE <= 0 {
		t.Fatalf("expected nonzero training error, got %g", calibration.trainingRMSE)
	}
}

func TestFitCalibrationRejectsInvalidSamples(t *testing.T) {
	valid := []Sample{{1, 2}, {2, 4}, {3, 6}}
	tests := []struct {
		name, reference, version string
		samples                  []Sample
	}{
		{"missing reference", "", "v1", valid},
		{"missing version", "ref", " ", valid},
		{"too few", "ref", "v1", valid[:2]},
		{"same cost", "ref", "v1", []Sample{{1, 1}, {1, 2}, {1, 3}}},
		{"negative cost", "ref", "v1", []Sample{{-1, 1}, {1, 2}, {2, 3}}},
		{"nonfinite time", "ref", "v1", []Sample{{1, 1}, {2, math.NaN()}, {3, 3}}},
		{"zero slope", "ref", "v1", []Sample{{1, 0}, {2, 0}, {3, 0}}},
		{"overflow", "ref", "v1", []Sample{{1, 1}, {2, 2}, {math.MaxFloat64, 3}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := FitCalibration(tt.reference, tt.version, tt.samples); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
