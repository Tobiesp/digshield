package digshield

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type testConnector struct {
	query string
	args  []driver.NamedValue
	rows  []string
	err   error
}

func (c *testConnector) Connect(context.Context) (driver.Conn, error) { return &testConn{c}, nil }
func (c *testConnector) Driver() driver.Driver                        { return testDriver{} }

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unexpected Open") }

type testConn struct{ connector *testConnector }

func (*testConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected Prepare") }
func (*testConn) Close() error                        { return nil }
func (*testConn) Begin() (driver.Tx, error)           { return nil, errors.New("unexpected Begin") }
func (c *testConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.connector.query, c.connector.args = query, args
	if c.connector.err != nil {
		return nil, c.connector.err
	}
	return &testRows{rows: c.connector.rows}, nil
}

type testRows struct {
	rows []string
	pos  int
}

func (*testRows) Columns() []string { return []string{"QUERY PLAN"} }
func (*testRows) Close() error      { return nil }
func (r *testRows) Next(dest []driver.Value) error {
	if r.pos == len(r.rows) {
		return io.EOF
	}
	dest[0] = r.rows[r.pos]
	r.pos++
	return nil
}

func testCalibration(t *testing.T) Calibration {
	t.Helper()
	c, err := FitCalibration("ref", "v1", []Sample{{1, 2}, {2, 4}, {3, 6}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPostgresEstimate(t *testing.T) {
	connector := &testConnector{rows: []string{`[{"Plan":{"Total Cost":2.5,"Plan Rows":12,"Plans":[{"Total Cost":200}]}}]`}}
	db := sql.OpenDB(connector)
	defer db.Close()
	estimator, err := NewPostgresEstimator(db, testCalibration(t))
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := estimator.Estimate(context.Background(), "SELECT $1 LIMIT $2", int64(7), int64(5))
	if err != nil {
		t.Fatal(err)
	}
	if connector.query != "EXPLAIN (FORMAT JSON) SELECT $1 LIMIT $2" {
		t.Fatalf("unexpected query %q", connector.query)
	}
	wantArgs := []driver.NamedValue{{Ordinal: 1, Value: int64(7)}, {Ordinal: 2, Value: int64(5)}}
	if !reflect.DeepEqual(connector.args, wantArgs) {
		t.Fatalf("arguments: got %#v, want %#v", connector.args, wantArgs)
	}
	want := Estimate{
		Database: "postgresql", NativeTotalCost: 2.5, EstimatedRows: 12,
		ReferenceMilliseconds: 5, Reference: "ref", CalibrationVersion: "v1", CalibrationSamples: 3,
	}
	if estimate != want {
		t.Fatalf("got %+v, want %+v", estimate, want)
	}
	connector.rows = []string{`[{"Plan":{"Total Cost":4,"Plan Rows":0}}]`}
	estimate, err = estimator.Estimate(context.Background(), "SELECT 1")
	if err != nil || !estimate.Extrapolated || estimate.ReferenceMilliseconds != 8 {
		t.Fatalf("extrapolated estimate: %+v, %v", estimate, err)
	}
	connector.rows = []string{`[{"Plan":{"Total Cost":0,"Plan Rows":0}}]`}
	estimate, err = estimator.Estimate(context.Background(), "SELECT 1")
	if err != nil || !estimate.Extrapolated || estimate.ReferenceMilliseconds != 0 {
		t.Fatalf("zero-cost estimate: %+v, %v", estimate, err)
	}
}

func TestPostgresErrors(t *testing.T) {
	connector := &testConnector{}
	db := sql.OpenDB(connector)
	defer db.Close()
	estimator, err := NewPostgresEstimator(db, testCalibration(t))
	if err != nil {
		t.Fatal(err)
	}
	var nilDB *sql.DB
	if _, err := NewPostgresEstimator(nilDB, testCalibration(t)); err == nil {
		t.Fatal("expected nil database error")
	}
	if _, err := NewPostgresEstimator(db, Calibration{}); err == nil {
		t.Fatal("expected invalid calibration error")
	}
	if _, err := estimator.Estimate(nil, "SELECT 1"); err == nil {
		t.Fatal("expected nil context error")
	}
	if _, err := estimator.Estimate(context.Background(), " "); err == nil {
		t.Fatal("expected empty query error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := estimator.Estimate(ctx, "SELECT 1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	sentinel := errors.New("database failure")
	connector.err = sentinel
	if _, err := estimator.Estimate(context.Background(), "SELECT 1"); !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped database error, got %v", err)
	}
	connector.err = nil
	for name, rows := range map[string][]string{
		"no rows":        nil,
		"multiple rows":  {`[]`, `[]`},
		"invalid JSON":   {`{`},
		"missing cost":   {`[{"Plan":{"Plan Rows":1}}]`},
		"negative rows":  {`[{"Plan":{"Total Cost":1,"Plan Rows":-1}}]`},
		"multiple plans": {`[{"Plan":{"Total Cost":1,"Plan Rows":1}},{"Plan":{"Total Cost":2,"Plan Rows":2}}]`},
		"null root":      {`[{"Plan":null}]`},
	} {
		t.Run(name, func(t *testing.T) {
			connector.rows = rows
			if _, err := estimator.Estimate(context.Background(), "SELECT 1"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if _, err := ExplainPostgres(context.Background(), nil, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("expected nil database error, got %v", err)
	}
}
