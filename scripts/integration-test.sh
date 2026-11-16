#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

engine="${CONTAINER_ENGINE:-podman}"
case "$engine" in
    podman|docker) ;;
    *)
        echo "CONTAINER_ENGINE must be podman or docker" >&2
        exit 1
        ;;
esac

container_id=
cleanup() {
    status=$?
    trap - EXIT
    if [ -n "$container_id" ]; then
        if ! "$engine" rm -f "$container_id" >/dev/null; then
            echo "Failed to remove integration-test container $container_id" >&2
            status=1
        else
            echo "Removed integration-test container $container_id"
        fi
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

container_id=$("$engine" run -d \
    -e POSTGRES_USER=digshield \
    -e POSTGRES_PASSWORD=digshield \
    -e POSTGRES_DB=digshield \
    -p 127.0.0.1::5432 \
    docker.io/library/postgres:17.5)
echo "Started integration-test container $container_id"

ready=false
for attempt in $(seq 1 60); do
    if "$engine" exec "$container_id" pg_isready -h 127.0.0.1 -U digshield -d digshield >/dev/null 2>&1; then
        ready=true
        break
    fi
    sleep 1
done
if [ "$ready" != true ]; then
    echo "PostgreSQL container did not become ready" >&2
    "$engine" logs "$container_id" >&2
    exit 1
fi

address=$("$engine" port "$container_id" 5432/tcp)
port=${address##*:}
case "$port" in
    ''|*[!0-9]*)
        echo "Could not determine PostgreSQL container port: $address" >&2
        exit 1
        ;;
esac

DIGSHIELD_TEST_POSTGRES_DSN="host=127.0.0.1 port=$port user=digshield password=digshield dbname=digshield sslmode=disable" \
    go test -v -tags=integration -run Integration -count=1 ./...
