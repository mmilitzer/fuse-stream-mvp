#!/bin/bash
set -euo pipefail

# Stress test harness for fuse-stream-mvp
# Runs tests multiple times with race detector and randomized GOMAXPROCS

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"

echo "========================================" 
echo "Stress Test Harness"
echo "========================================" 
echo ""

# Configuration
ITERATIONS=${ITERATIONS:-100}
MAX_PROCS=$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

echo "Configuration:"
echo "  Iterations: $ITERATIONS"
echo "  Max GOMAXPROCS: $MAX_PROCS"
echo ""

# Test 1: Run with race detector
echo "[1/4] Running tests with race detector..."
if ! go test -race -count="$ITERATIONS" -timeout=30m ./internal/fetcher/...; then
    echo "ERROR: Race detector found issues!"
    exit 1
fi
echo "✓ Race detector tests passed"
echo ""

# Test 2: Run with randomized GOMAXPROCS
echo "[2/4] Running tests with randomized GOMAXPROCS..."
for i in $(seq 1 "$ITERATIONS"); do
    PROCS=$((RANDOM % MAX_PROCS + 1))
    echo "  Iteration $i/$ITERATIONS: GOMAXPROCS=$PROCS"
    if ! GOMAXPROCS="$PROCS" go test -timeout=10m ./internal/fetcher/... > /dev/null; then
        echo "ERROR: Test failed at iteration $i with GOMAXPROCS=$PROCS"
        exit 1
    fi
done
echo "✓ Randomized GOMAXPROCS tests passed"
echo ""

# Test 3: Run stress test with race detector
echo "[3/4] Running stress tests with race detector..."
if ! go test -race -run=TestStressWithRandomGOMAXPROCS -timeout=15m ./internal/fetcher/...; then
    echo "ERROR: Stress test with race detector failed!"
    exit 1
fi
echo "✓ Stress tests with race detector passed"
echo ""

# Test 4: Run with high concurrency
echo "[4/4] Running tests with high concurrency..."
if ! GOMAXPROCS="$MAX_PROCS" go test -parallel="$MAX_PROCS" -count=10 -timeout=15m ./internal/fetcher/...; then
    echo "ERROR: High concurrency test failed!"
    exit 1
fi
echo "✓ High concurrency tests passed"
echo ""

echo "========================================" 
echo "All stress tests passed! ✓"
echo "========================================" 
