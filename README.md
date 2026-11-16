# digshield

Digshield estimates the cost of a SQL query without executing it. It reports
PostgreSQL's native planner cost and an estimated execution time in
milliseconds on a **caller-defined reference environment**. The calling
application decides whether and how to use the estimate; digshield does not
reject or execute queries. GraphQL and other database engines are not yet
implemented.

Go 1.24 or newer is required. The application supplies and owns a
`database/sql` connection and registers its PostgreSQL driver.

## Calibrate

PostgreSQL's cost units are not milliseconds and cannot be compared directly
to other databases' costs. There is no built-in conversion: supply measurements
from representative queries executed on a defined reference environment.
For each query and argument set, record the root `TotalCost` from
`ExplainPostgres` and the separately measured execution time in milliseconds.
Use a dedicated benchmark database with representative data, statistics,
indexes, server settings, hardware, driver, and query mix. Fully consume results,
define whether transfer and client time are included, and use consistent
warm-up, repetitions and concurrency. Benchmark only queries that are safe to
execute, with appropriate permissions and timeouts; do not benchmark production
traffic implicitly.

`FitCalibration` fits `milliseconds = total_cost * slope` through the origin.
It requires at least three measurements with different costs. This deliberately
simple model may be inaccurate even within the sampled range: planner cost
does not account for all real execution costs. Validate it on held-out
representative workloads before using the estimate. The returned training
RMSE measures fit to the supplied samples, **not** a confidence interval or a
guarantee. Recalibrate when the environment, database configuration, data,
statistics or workload changes. Record the measurements and their methodology
so the calibration can be recreated. The reference ID must identify that
environment and methodology; give each calibration a distinct version.

```go
// Illustrative numbers only: replace with actual reference measurements.
samples := []digshield.Sample{
    {TotalCost: 10, ElapsedMilliseconds: 2},
    {TotalCost: 20, ElapsedMilliseconds: 4},
    {TotalCost: 30, ElapsedMilliseconds: 6},
}
calibration, err := digshield.FitCalibration("reference-host-and-workload-v1", "postgres-model-v1", samples)
if err != nil {
    return err
}
estimator, err := digshield.NewPostgresEstimator(db, calibration)
if err != nil {
    return err
}
```

For the calibration to apply, the estimated query's data, schema, statistics
and planner settings must be representative of those used to collect the
reference samples. In particular, applying a reference model to a plan from a
different database configuration does not automatically predict its reference
runtime.

In the future, independently calibrated database adapters could report
reference milliseconds on the same reference environment and methodology.
Comparable labels alone do not prove comparable accuracy: validate each model
on the relevant workloads. Currently only PostgreSQL is supported.

## Estimate a Digcommander statement

`digcommander.Build(schema, request)` returns the SQL and ordered arguments.
Build and estimate the same statement the application intends to execute:

```go
statement, err := digcommander.Build(schema, request)
if err != nil {
    return err
}
estimate, err := estimator.Estimate(ctx, statement.SQL, statement.Args...)
if err != nil {
    return err
}
fmt.Printf("reference milliseconds: %g; native total cost: %g; extrapolated: %t\n",
    estimate.ReferenceMilliseconds, estimate.NativeTotalCost, estimate.Extrapolated)
```

`Extrapolated` indicates that the plan's cost is outside the calibration sample
range; it does not indicate accuracy inside that range. The estimate also
includes the reference ID, calibration version, sample count, training RMSE
and estimated root row count. Never compare reference milliseconds as though
they were measurements on the current deployment unless it matches the
reference and the model has been validated.

Digcommander's `Query` builds the statement again. If it is used after
estimation, preserve the schema, request and arguments unchanged and use the
same database/session settings; otherwise the executed statement or plan can
differ. Statistics, data, settings or plans may also change between estimate
and execution. A `*sql.Conn` or `*sql.Tx` can preserve session context; the
caller manages its lifetime.

## Safety and limitations

Pass only trusted, single SELECT statements, such as those built by
Digcommander. The SQL text is not parsed or validated by digshield; never pass
untrusted SQL. Bound arguments are forwarded separately and are not
interpolated. The connection must target the database and session whose plan
is relevant. PostgreSQL planning can still invoke functions in some
circumstances, so `EXPLAIN` is not a security sandbox. Use least privilege,
appropriate timeouts and database-side resource controls. Planner estimates
can be wrong, planning has overhead, and estimating a query does not ensure
that it will finish within any time or resource limit.

## Tests

Run unit tests with `go test ./...` and `go vet ./...`. Run integration tests
locally with a running Podman machine:

```sh
sh scripts/integration-test.sh
```

The script starts an isolated PostgreSQL 17.5 container on an ephemeral
localhost port and removes it afterward. The image is pulled if necessary.
Integration tests are excluded from ordinary `go test` runs. CI uses the same
script with Docker (`CONTAINER_ENGINE=docker`). Do not supply a production
database to integration tests.
