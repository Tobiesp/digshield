package digshield

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Queryer is implemented by *sql.DB, *sql.Conn, and *sql.Tx.
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// PostgresPlan contains estimates from the root of a PostgreSQL plan.
type PostgresPlan struct {
	TotalCost     float64
	EstimatedRows float64
}

// Estimate describes a PostgreSQL plan and its calibrated reference time.
type Estimate struct {
	Database              string
	NativeTotalCost       float64
	EstimatedRows         float64
	ReferenceMilliseconds float64
	Reference             string
	CalibrationVersion    string
	CalibrationSamples    int
	TrainingRMSEMillis    float64
	Extrapolated          bool
}

// PostgresEstimator estimates PostgreSQL queries using a fitted calibration.
type PostgresEstimator struct {
	db          Queryer
	calibration Calibration
}

// NewPostgresEstimator uses the caller-owned connection and calibration.
func NewPostgresEstimator(db Queryer, calibration Calibration) (*PostgresEstimator, error) {
	if db == nil || isNilQueryer(db) {
		return nil, errors.New("database connection is nil")
	}
	if calibration.samples < 3 || calibration.millisecondsPerCost <= 0 {
		return nil, errors.New("invalid calibration")
	}
	return &PostgresEstimator{db: db, calibration: calibration}, nil
}

func isNilQueryer(db Queryer) bool {
	value := reflect.ValueOf(db)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Estimate runs EXPLAIN, not the query itself. SQL must be a trusted, single
// SELECT, such as a statement produced by digcommander.Build.
func (e *PostgresEstimator) Estimate(ctx context.Context, query string, args ...any) (Estimate, error) {
	if e == nil || e.db == nil {
		return Estimate{}, errors.New("estimator is nil or uninitialized")
	}
	plan, err := ExplainPostgres(ctx, e.db, query, args...)
	if err != nil {
		return Estimate{}, err
	}
	c := e.calibration
	milliseconds := plan.TotalCost * c.millisecondsPerCost
	if !finiteNonnegative(milliseconds) {
		return Estimate{}, errors.New("calibrated estimate overflows")
	}
	return Estimate{
		Database: "postgresql", NativeTotalCost: plan.TotalCost, EstimatedRows: plan.EstimatedRows,
		ReferenceMilliseconds: milliseconds, Reference: c.reference, CalibrationVersion: c.version,
		CalibrationSamples: c.samples, TrainingRMSEMillis: c.trainingRMSE,
		Extrapolated: plan.TotalCost < c.minCost || plan.TotalCost > c.maxCost,
	}, nil
}

// ExplainPostgres obtains native planner estimates without requiring a
// calibration. SQL must be a trusted, single SELECT; it is not validated.
func ExplainPostgres(ctx context.Context, db Queryer, query string, args ...any) (PostgresPlan, error) {
	if db == nil || isNilQueryer(db) {
		return PostgresPlan{}, errors.New("database connection is nil")
	}
	if ctx == nil {
		return PostgresPlan{}, errors.New("context is nil")
	}
	if strings.TrimSpace(query) == "" {
		return PostgresPlan{}, errors.New("query is empty")
	}
	rows, err := db.QueryContext(ctx, "EXPLAIN (FORMAT JSON) "+query, args...)
	if err != nil {
		return PostgresPlan{}, fmt.Errorf("explain PostgreSQL query: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return PostgresPlan{}, fmt.Errorf("read PostgreSQL plan: %w", err)
		}
		return PostgresPlan{}, errors.New("PostgreSQL EXPLAIN returned no plan")
	}
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		return PostgresPlan{}, fmt.Errorf("scan PostgreSQL plan: %w", err)
	}
	if rows.Next() {
		return PostgresPlan{}, errors.New("PostgreSQL EXPLAIN returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return PostgresPlan{}, fmt.Errorf("read PostgreSQL plan: %w", err)
	}
	cost, estimatedRows, err := parsePostgresPlan(raw)
	if err != nil {
		return PostgresPlan{}, err
	}
	return PostgresPlan{TotalCost: cost, EstimatedRows: estimatedRows}, nil
}

func parsePostgresPlan(raw []byte) (float64, float64, error) {
	var plans []struct {
		Plan *struct {
			TotalCost *float64 `json:"Total Cost"`
			PlanRows  *float64 `json:"Plan Rows"`
		} `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil {
		return 0, 0, fmt.Errorf("decode PostgreSQL plan: %w", err)
	}
	if len(plans) != 1 || plans[0].Plan == nil ||
		plans[0].Plan.TotalCost == nil || plans[0].Plan.PlanRows == nil ||
		!finiteNonnegative(*plans[0].Plan.TotalCost) || !finiteNonnegative(*plans[0].Plan.PlanRows) {
		return 0, 0, errors.New("invalid PostgreSQL plan: expected one root with nonnegative total cost and rows")
	}
	return *plans[0].Plan.TotalCost, *plans[0].Plan.PlanRows, nil
}
