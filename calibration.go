// Package digshield estimates the cost of SQL queries without executing them.
package digshield

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Sample pairs a PostgreSQL root plan's total cost with an observed execution
// time on the reference environment, in milliseconds.
type Sample struct {
	TotalCost           float64
	ElapsedMilliseconds float64
}

// Calibration is a fitted PostgreSQL cost-to-time model. Its zero value is invalid.
type Calibration struct {
	reference, version  string
	millisecondsPerCost float64
	minCost, maxCost    float64
	trainingRMSE        float64
	samples             int
}

// FitCalibration fits a line through the origin to at least three reference
// measurements. Reference and version must identify the benchmark environment
// and this specific calibration so results can be interpreted and compared.
func FitCalibration(reference, version string, samples []Sample) (Calibration, error) {
	if strings.TrimSpace(reference) == "" || strings.TrimSpace(version) == "" {
		return Calibration{}, errors.New("reference and calibration version must be nonempty")
	}
	if len(samples) < 3 {
		return Calibration{}, errors.New("calibration requires at least three samples")
	}

	minCost, maxCost := math.Inf(1), 0.0
	var sumSquares, sumProducts float64
	for i, sample := range samples {
		if !finiteNonnegative(sample.TotalCost) || !finiteNonnegative(sample.ElapsedMilliseconds) {
			return Calibration{}, fmt.Errorf("sample %d: cost and elapsed time must be finite and nonnegative", i)
		}
		minCost = math.Min(minCost, sample.TotalCost)
		maxCost = math.Max(maxCost, sample.TotalCost)
		sumSquares += sample.TotalCost * sample.TotalCost
		sumProducts += sample.TotalCost * sample.ElapsedMilliseconds
	}
	if minCost == maxCost || sumSquares == 0 || !finiteNonnegative(sumSquares) || !finiteNonnegative(sumProducts) {
		return Calibration{}, errors.New("calibration requires distinct costs and finite fitting values")
	}
	slope := sumProducts / sumSquares
	if !finiteNonnegative(slope) || slope == 0 {
		return Calibration{}, errors.New("calibration requires a positive finite fitted slope")
	}
	var squaredError float64
	for _, sample := range samples {
		residual := sample.ElapsedMilliseconds - slope*sample.TotalCost
		squaredError += residual * residual
	}
	rmse := math.Sqrt(squaredError / float64(len(samples)))
	if !finiteNonnegative(rmse) {
		return Calibration{}, errors.New("calibration residuals overflow")
	}
	return Calibration{
		reference: reference, version: version, millisecondsPerCost: slope,
		minCost: minCost, maxCost: maxCost, trainingRMSE: rmse, samples: len(samples),
	}, nil
}

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}
