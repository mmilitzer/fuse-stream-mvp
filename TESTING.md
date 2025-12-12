# Testing Guide for TempFileStore Concurrency Fixes

## Quick Start

### Run All Tests
```bash
make test
```

### Run with Race Detector
```bash
make test-race
```

### Run Static Analysis
```bash
make lint
```

### Run Full CI Suite Locally
```bash
make ci
```

## Test Categories

### 1. Unit Tests (Fast)
Basic functionality tests without heavy concurrency:
```bash
go test -short ./internal/fetcher/...
```

### 2. Concurrency Tests
Focused tests for multi-threaded safety:
```bash
go test -run TestConcurrent ./internal/fetcher/...
```

Individual tests:
- `TestConcurrentReaders` - Multiple goroutines reading simultaneously
- `TestReadCloseConcurrent` - Close while reads are active
- `TestMultiFileParallelism` - Multiple stores in parallel
- `TestStressWithRandomGOMAXPROCS` - Randomized scheduler stress test

### 3. Race Detector Tests
Detects data races at runtime (slower, ~10x):
```bash
go test -race ./internal/fetcher/...
```

### 4. Stress Tests
Run tests many times to expose rare race conditions:
```bash
./scripts/stress-test.sh
```

Customize iteration count:
```bash
ITERATIONS=500 ./scripts/stress-test.sh
```

## Debugging Concurrency Issues

### Reading Log Output

Example log:
```
[TempFileStore #1] ReadAt ENTER off=1048576 len=131072 goid=42
[TempFileStore #1] ReadAt EXIT off=1048576 len=131072 goid=42
```

- `#1` = Store debug ID (unique per TempFileStore instance)
- `goid=42` = Goroutine ID
- `off=1048576` = Read offset
- `len=131072` = Read length

### Detecting Deadlocks

Look for ENTER without corresponding EXIT:
```
[TempFileStore #1] ReadAt ENTER off=1048576 len=131072 goid=42
[TempFileStore #1] ReadAt ENTER off=2097152 len=131072 goid=43
[TempFileStore #1] ReadAt EXIT off=2097152 len=131072 goid=43
# Missing EXIT for goid=42 - indicates hang!
```

### Detecting Concurrent Operations

Multiple ENTER logs before first EXIT indicates concurrency:
```
[TempFileStore #1] ReadAt ENTER off=1048576 len=131072 goid=42
[TempFileStore #1] ReadAt ENTER off=2097152 len=131072 goid=43  <- concurrent!
[TempFileStore #1] ReadAt EXIT off=2097152 len=131072 goid=43
[TempFileStore #1] ReadAt EXIT off=1048576 len=131072 goid=42
```

## Race Detector Output

Example race detector finding:
```
==================
WARNING: DATA RACE
Read at 0x00c000012345 by goroutine 7:
  github.com/mmilitzer/fuse-stream-mvp/internal/fetcher.(*TempFileStore).ReadAt()
      store_tempfile.go:283 +0x123

Previous write at 0x00c000012345 by goroutine 6:
  github.com/mmilitzer/fuse-stream-mvp/internal/fetcher.(*TempFileStore).downloader()
      store_tempfile.go:169 +0x456
==================
```

This shows:
1. **What:** Data race on memory at 0x00c000012345
2. **Where:** Line 283 in ReadAt (goroutine 7) racing with line 169 in downloader (goroutine 6)
3. **When:** One goroutine reading while another writing

## Common Issues

### Test Timeout
If tests hang, it's likely a deadlock. Check logs for missing EXIT statements.

### Race Detector Failures
If race detector reports issues, fix them immediately - they represent real bugs that will cause production failures.

### Flaky Tests
If a test passes sometimes and fails others, it's likely a race condition. Run with:
```bash
go test -race -count=100 -run TestName ./internal/fetcher/...
```

## CI Integration

GitHub Actions runs three checks:

### 1. Static Analysis
```yaml
- go vet ./internal/...
- staticcheck ./internal/...
```
**Fails if:** Code has linting issues

### 2. Unit Tests
```yaml
- go test ./... -tags fuse
```
**Fails if:** Any test fails

### 3. Race Detector
```yaml
- go test -race -timeout=25m ./internal/...
```
**Fails if:** Race detector finds data races

## Best Practices

1. **Always run with race detector** before pushing code
2. **Run stress tests** for critical concurrent code changes
3. **Check logs** for ENTER/EXIT patterns to verify operations complete
4. **Test with different GOMAXPROCS** to expose scheduling bugs
5. **Use timeouts** in tests to prevent infinite hangs

## Performance Notes

### Test Execution Times

| Test Type | Duration |
|-----------|----------|
| Unit tests | ~5s |
| Concurrency tests | ~30s |
| Race detector tests | ~5min |
| Stress tests | ~10-30min |

### Race Detector Overhead
- **CPU:** ~10x slower
- **Memory:** ~10x more
- **Why:** Instruments every memory access

### Recommendations
- Use `-short` for quick feedback during development
- Use `-race` before committing changes
- Use stress tests before major releases
- CI runs full suite including race detector

## Troubleshooting

### Go Not Found
```bash
# macOS
brew install go

# Linux
sudo apt-get install golang-go

# Or download from https://go.dev/dl/
```

### Staticcheck Not Found
```bash
go install honnef.co/go/tools/cmd/staticcheck@latest
```

### Tests Fail in CI but Pass Locally
- Could be timing-dependent race condition
- Run with `-race` and `-count=100` locally
- Try with different GOMAXPROCS values

### Memory Issues with Race Detector
Race detector uses more memory. If tests fail with OOM:
```bash
# Run tests individually
go test -race -run TestConcurrentReaders ./internal/fetcher/...
go test -race -run TestReadCloseConcurrent ./internal/fetcher/...
```

## Additional Resources

- [Go Race Detector](https://go.dev/doc/articles/race_detector)
- [Go Testing](https://go.dev/doc/tutorial/add-a-test)
- [staticcheck](https://staticcheck.io/)
- [CONCURRENCY_FIX.md](CONCURRENCY_FIX.md) - Detailed explanation of fixes
