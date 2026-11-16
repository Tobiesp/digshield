//go:build integration

package digshield

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("DIGSHIELD_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("run integration tests with sh scripts/integration-test.sh")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `CREATE TEMP TABLE digshield_test (id integer);
		INSERT INTO digshield_test VALUES (1), (2);
		CREATE TEMP SEQUENCE digshield_sequence`); err != nil {
		t.Fatal(err)
	}
	estimator, err := NewPostgresEstimator(conn, testCalibration(t))
	if err != nil {
		t.Fatal(err)
	}
	query := `SELECT "_dig_relation"."id" FROM "pg_temp"."digshield_test" AS "_dig_relation" WHERE "_dig_relation"."id" = $1 LIMIT $2`
	estimate, err := estimator.Estimate(ctx, query, int64(1), int64(10))
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Database != "postgresql" || estimate.NativeTotalCost <= 0 || estimate.ReferenceMilliseconds <= 0 {
		t.Fatalf("unexpected estimate: %+v", estimate)
	}
	if _, err := ExplainPostgres(ctx, conn, `SELECT nextval('pg_temp.digshield_sequence')`); err != nil {
		t.Fatal(err)
	}
	var called bool
	if err := conn.QueryRowContext(ctx, `SELECT is_called FROM pg_temp.digshield_sequence`).Scan(&called); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("EXPLAIN executed the query")
	}
}
