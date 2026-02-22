# Test-First Debugging Strategy

This document outlines the comprehensive testing strategy for the AI SRE Agent project, focusing on test-first debugging practices to improve stability and reduce regression risk.

## Table of Contents

1. [Philosophy](#philosophy)
2. [Current Test Coverage](#current-test-coverage)
3. [Test-First Debugging Workflow](#test-first-debugging-workflow)
4. [Test Layers and Responsibilities](#test-layers-and-responsibilities)
5. [Priority Areas for Testing](#priority-areas-for-testing)
6. [Test Templates and Patterns](#test-templates-and-patterns)
7. [Running Tests and Interpreting Failures](#running-tests-and-interpreting-failures)
8. [Continuous Integration Strategy](#continuous-integration-strategy)

## Philosophy

### Test-First Debugging Loop

1. **Reproduce** the issue with an automated test
2. **Confirm** the test fails for the right reason
3. **Implement** the smallest fix that makes the test pass
4. **Verify** by running nearby and broader test suites
5. **Keep** the new test as a regression guard

### Core Principles

- **Tests before fixes**: Never fix a bug without first writing a test that exposes it
- **Defensive programming**: Tests validate error handling and edge cases, not just happy paths
- **Fast feedback**: Unit tests should run in milliseconds, integration tests in seconds
- **Isolation**: Each test should be independent and not rely on test execution order
- **Maintainability**: Tests should be as readable and maintainable as production code

## Current Test Coverage

### Module Coverage Analysis

As of 2026-02-21:

| Module | Main Files | Test Files | Coverage | Priority |
|--------|-----------|------------|----------|----------|
| controller | 50 | 29 | 58% | High |
| collector | 10 | 10 | 100% | - |
| core | 4 | 3 | 75% | Medium |
| agent | 3 | 3 | 100% | - |
| **probe** | **17** | **2** | **11%** | **Critical** |
| **monitoring** | **20** | **1** | **5%** | **High** |
| incidents | 5 | 1 | 20% | Medium |
| security | 3 | 1 | 33% | High |
| middleware | 3 | 1 | 33% | Medium |
| **store** | 3 | 0 | 0% | High |
| **services** | 1 | 0 | 0% | Medium |
| **remediation** | 4 | 0 | 0% | High |
| **platform** | 2 | 0 | 0% | Medium |
| **observability** | 4 | 0 | 0% | Medium |
| finops | 2 | 0 | 0% | Low |
| change | 1 | 0 | 0% | Low |
| brain | 3 | 0 | 0% | Low |
| alerting | 3 | 0 | 0% | Medium |

### Coverage Goals

- **Critical path**: 80%+ coverage (probe, monitoring, controller, store)
- **High priority**: 60%+ coverage (security, remediation, incidents)
- **Medium priority**: 40%+ coverage (remaining modules)
- **Low priority**: 20%+ coverage (experimental/optional features)

## Test-First Debugging Workflow

### Step 1: Reproduce the Issue

Write a test that captures the bug:

```go
func TestCollectorHandlesNilMetricPoints(t *testing.T) {
    // Setup
    collector := setupTestCollector(t)
    defer collector.Stop()

    // Execute: Try to collect a batch with nil points
    batch := &telemetryv1.TelemetryBatch{
        Metrics: []*telemetryv1.Metric{
            {
                Name:  "test_metric",
                Value: 42,
                // Points is nil - this should be handled gracefully
            },
        },
    }

    // Verify: Should not panic, should skip the metric
    err := collector.ProcessBatch(batch)
    require.NoError(t, err, "Processing batch with nil points should not error")
}
```

### Step 2: Confirm the Test Fails

Run the test and verify it fails for the expected reason:

```bash
go test ./internal/collector -run TestCollectorHandlesNilMetricPoints -v
```

Expected output:
```
=== RUN   TestCollectorHandlesNilMetricPoints
    collector_test.go:123: panic: runtime error: invalid memory address
--- FAIL: TestCollectorHandlesNilMetricPoints (0.00s)
```

### Step 3: Implement the Fix

Make the minimal change to fix the issue:

```go
func (c *Collector) ProcessBatch(batch *telemetryv1.TelemetryBatch) error {
    for _, metric := range batch.Metrics {
        if metric == nil || !isValidMetric(metric) {
            continue  // Skip invalid metrics instead of panicking
        }
        // ... process metric
    }
    return nil
}
```

### Step 4: Verify the Fix

Run the test again and verify it passes:

```bash
go test ./internal/collector -run TestCollectorHandlesNilMetricPoints -v
```

Expected output:
```
=== RUN   TestCollectorHandlesNilMetricPoints
--- PASS: TestCollectorHandlesNilMetricPoints (0.00s)
PASS
```

### Step 5: Run Broader Test Suites

Ensure the fix doesn't break related functionality:

```bash
# Run nearby tests
go test ./internal/collector -v

# Run integration tests
go test ./internal/monitoring ./internal/controller/ingest -v

# Run full suite
go test ./... -v
```

### Step 6: Add Regression Guard Documentation

Document the test in the code:

```go
// TestCollectorHandlesNilMetricPoints verifies that the collector gracefully
// handles metrics with nil point arrays, which can occur during protobuf
// deserialization or when external metrics sources provide malformed data.
//
// Regression test for: https://github.com/jfang2048/ai_sre_agent/issues/123
func TestCollectorHandlesNilMetricPoints(t *testing.T) {
    // ... test implementation
}
```

## Test Layers and Responsibilities

### 1. Unit Tests

**Goal**: Validate deterministic logic and edge cases in isolation

**Characteristics**:
- Fast (milliseconds)
- No external dependencies (use mocks/fakes)
- Test single functions or methods
- Cover edge cases and error conditions

**Examples**:
```go
// Test pure function
func TestValidateLabelRejectsNil(t *testing.T) {
    err := validateLabel(nil)
    require.Error(t, err)
    require.Contains(t, err.Error(), "cannot be nil")
}

// Test state machine
func TestSLOStateMachineRejectsInvalidTransition(t *testing.T) {
    sm := NewSLOStateMachine()
    sm.TransitionTo(StateCompliant)
    err := sm.TransitionTo(StateUnknown)  // Invalid transition
    require.Error(t, err)
}

// Test data structure
func TestMetricsHistoryDeduplicatesKeys(t *testing.T) {
    history := NewMetricsHistory()
    history.Set("cpu", 1.0, time.Now())
    history.Set("cpu", 2.0, time.Now().Add(time.Second))
    require.Equal(t, 1, len(history.GetLatest()), "Should deduplicate by key")
}
```

### 2. Integration Tests

**Goal**: Validate multi-component data flow and contracts

**Characteristics**:
- Slower (seconds)
- Real components (no mocks)
- Test interactions between 2-5 components
- Validate contracts and data formats

**Examples**:
```go
// Test collector -> ingest -> store flow
func TestCollectorIngestStoreE2E(t *testing.T) {
    store := NewStore()
    server := NewIngestServer(store)
    client := NewTestClient(server)

    // Send real telemetry batch
    batch := &telemetryv1.TelemetryBatch{
        Collector: &telemetryv1.CollectorInfo{CollectorId: "test"},
        Metrics:   []*telemetryv1.Metric{{Name: "cpu", Value: 42}},
    }
    err := client.Send(batch)
    require.NoError(t, err)

    // Verify stored data
    node := store.Node("test")
    require.NotNil(t, node)
    require.Equal(t, 1, node.MetricCount)
}
```

### 3. End-to-End Tests

**Goal**: Validate complete workflows from user input to system output

**Characteristics**:
- Slowest (tens of seconds)
- Full system stack
- Test critical user journeys
- Validate system-wide behavior

**Examples**:
```go
// Test probe -> controller -> API workflow
func TestProbeControllerWorkflowE2E(t *testing.T) {
    // Start controller
    controller := NewTestController()
    defer controller.Stop()

    // Start collector
    collector := NewTestCollector(controller.Endpoint())
    defer collector.Stop()

    // Collect and send telemetry
    collector.CollectOnce()

    // Verify API returns data
    resp := httptest.NewRequest("GET", "/api/v1/fleet", nil)
    w := httptest.NewRecorder()
    controller.ServeHTTP(w, resp)
    require.Equal(t, 200, w.Code)
    require.Contains(t, w.Body.String(), collector.ID())
}
```

## Priority Areas for Testing

### Critical Path (Immediate Priority)

1. **Probe Module (11% coverage)**
   - Core data collection logic
   - System calls and file reading
   - Error handling for missing/unavailable data sources
   - Metric validation and sanitization

2. **Monitoring Module (5% coverage)**
   - SLI/SLO calculation
   - Metrics aggregation
   - Trend analysis
   - Performance monitoring

3. **Store Module (0% coverage)**
   - In-memory data storage
   - Concurrent access safety
   - Data retention and cleanup
   - Snapshot and query operations

### High Priority

4. **Security Module (33% coverage)**
   - Authentication and authorization
   - Secret handling
   - Input validation
   - Audit logging

5. **Remediation Module (0% coverage)**
   - Remediation action execution
   - Safety checks and guards
   - Rollback mechanisms
   - Dry-run mode

6. **Controller Module (58% coverage)**
   - API handlers
   - Data aggregation
   - Diagnostic logic
   - Kubernetes integration

## Test Templates and Patterns

### Template 1: Nil-Safety Test

```go
func Test[Component]HandlesNil[Input](t *testing.T) {
    component := New[Component]()
    err := component.Process(nil)
    require.NoError(t, err, "Should handle nil input gracefully")
}
```

### Template 2: Error Recovery Test

```go
func Test[Component]RecoversFrom[Error](t *testing.T) {
    component := New[Component]()

    // Cause error
    err := component.Process(invalidInput)
    require.Error(t, err)

    // Verify recovery
    err = component.Process(validInput)
    require.NoError(t, err, "Should recover after error")
}
```

### Template 3: Concurrent Access Test

```go
func Test[Component]ThreadSafe(t *testing.T) {
    component := New[Component]()
    done := make(chan bool)

    // Concurrent writers
    for i := 0; i < 10; i++ {
        go func() {
            component.Update("key", i)
            done <- true
        }()
    }

    // Wait for all goroutines
    for i := 0; i < 10; i++ {
        <-done
    }

    // Verify no corruption or panics
    value := component.Get("key")
    require.NotNil(t, value)
}
```

### Template 4: Boundary Condition Test

```go
func Test[Component]HandlesBoundaryConditions(t *testing.T) {
    component := New[Component]()

    testCases := []struct {
        name  string
        input interface{}
        valid bool
    }{
        {"empty string", "", false},
        {"max length", strings.Repeat("a", 1000), false},
        {"special chars", "\n\r\t", false},
        {"valid input", "normal", true},
    }

    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            err := component.Process(tc.input)
            if tc.valid {
                require.NoError(t, err)
            } else {
                require.Error(t, err)
            }
        })
    }
}
```

### Template 5: Integration Test with Setup/Teardown

```go
func setupTestIngestPipeline(t *testing.T) (*Store, *Server, *Client) {
    t.Helper()
    store := NewStore()
    server := NewServer(store, testLogger())
    client := NewTestClient(server)

    t.Cleanup(func() {
        client.Close()
        server.Stop()
    })

    return store, server, client
}

func TestIngestPipelineWithValidBatch(t *testing.T) {
    store, server, client := setupTestIngestPipeline(t)

    batch := &telemetryv1.TelemetryBatch{
        Collector: &telemetryv1.CollectorInfo{CollectorId: "test"},
        Metrics:   []*telemetryv1.Metric{{Name: "cpu", Value: 42}},
    }

    err := client.Send(batch)
    require.NoError(t, err)

    node := store.Node("test")
    require.NotNil(t, node)
    require.Equal(t, 1, node.MetricCount)
}
```

## Running Tests and Interpreting Failures

### Quick Test Commands

```bash
# Run the full layered stability workflow
make test-stability

# Run all tests
cd backend && go test ./...

# Run external integration pipeline tests
cd tests/integration && go test -v .

# Run external-stack E2E tests (requires local stack or skips)
cd tests/e2e && go test -v -tags=e2e .

# Run Python unit tests
python3 -m unittest discover -s tests/python -p 'test_*.py'

# Run specific package
go test ./internal/probe

# Run specific test
go test ./internal/collector -run TestCollectorHandlesNilMetricPoints

# Run with race detector
go test -race ./internal/core

# Run with coverage
go test -cover ./...

# Run with coverage profile
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Verbose output
go test -v ./internal/controller
```

Notes:
- `tests/integration` uses in-memory gRPC (`bufconn`) so it remains runnable in restricted environments without TCP bind permissions.
- `tests/e2e` targets a live local stack and may skip when preconditions are unmet.
- `tests/python` uses `unittest` discovery so it runs without requiring `pytest` in minimal environments.
- `scripts/ci.sh` builds frontend output to `/tmp/ai_sre_agent_frontend_build` by default; set `SRE_FRONTEND_BUILD_OUTDIR` to override.
- Cross-package suite boundaries and shared helper locations are documented in `tests/README.md`.

### Common Failure Patterns

#### Pattern 1: Race Condition

```
WARNING: DATA RACE
Read at 0x... by goroutine ...:
  ...
Previous write at 0x... by goroutine ...:
  ...
```

**Interpretation**: Unsynchronized access to shared state

**Solution**: Add proper synchronization (mutex, channels, atomic operations)

#### Pattern 2: Nil Pointer Dereference

```
panic: runtime error: invalid memory address or nil pointer dereference
```

**Interpretation**: Missing nil check before using a pointer

**Solution**: Add defensive nil checks or use nil-safe patterns

#### Pattern 3: Test Timeout

```
--- FAIL: TestSomething (10.00s)
    test_test.go:123: function took too long
```

**Interpretation**: Infinite loop, deadlock, or very slow operation

**Solution**: Add timeouts to context, fix deadlock, optimize operation

#### Pattern 4: Flaky Test

```
--- FAIL: TestSomething (0.00s)
    test_test.go:123: expected 42, got 41
```

**Interpretation**: Test depends on external state or timing

**Solution**: Eliminate external dependencies, add proper mocking, fix race condition

#### Pattern 5: Environment-Guarded Skip

```
--- SKIP: TestAgentFlowE2E
    skipping e2e: controller unavailable or sockets restricted (...)
```

**Interpretation**: Runner does not satisfy E2E preconditions (local stack not running, or socket policy blocks networking)

**Solution**: Start `./scripts/run-local.sh --enable-agent` and rerun on a runner with local socket access

### Failure Triage Guide

| Module | Common Issues | Debugging Commands |
|--------|--------------|-------------------|
| `internal/core` | Logic errors, state inconsistency | `go test -v ./internal/core -run TestName` |
| `internal/monitoring` | Nil pointer, race conditions | `go test -race ./internal/monitoring` |
| `internal/controller/ingest` | Validation errors, schema drift | `go test ./internal/controller/ingest -v` |
| `internal/probe` | System call failures, missing files | `strace -f go test ./internal/probe` |
| `frontend` | API contract drift, rendering issues | `npm test -- --no-coverage` |

## Continuous Integration Strategy

### CI Test Stages

#### Stage 1: Fast Feedback (Parallel, < 2 minutes)

- Unit tests for all modules
- Lint checks (`golangci-lint`, `eslint`)
- Format checks (`gofmt`, `prettier`)
- Basic validation (schema checks, compilation)

```yaml
# .github/workflows/test.yml
fast-checks:
  runs-on: ubuntu-latest
  steps:
    - name: Run unit tests
      run: go test ./... -short
    - name: Run linter
      run: golangci-lint run
```

#### Stage 2: Integration Tests (Parallel, < 10 minutes)

- Integration tests for data pipeline
- API contract tests
- Database/integration tests
- Security scanning (SAST, dependency vulnerabilities)

```yaml
integration-tests:
  runs-on: ubuntu-latest
  steps:
    - name: Run integration tests
      run: go test ./internal/monitoring ./internal/controller/ingest -count=1
    - name: Run security scan
      run: gosec ./...
```

#### Stage 3: E2E Tests (Sequential, < 20 minutes)

- Full system tests
- Probe-controller workflow tests
- UI E2E tests (Playwright/Cypress)
- Performance benchmarks

```yaml
e2e-tests:
  runs-on: ubuntu-latest
  steps:
    - name: Run E2E tests
      run: go test ./internal/controller -run ".*E2E$" -count=1
    - name: Run UI tests
      run: cd frontend && npm test
```

### Coverage Requirements

| Module Type | Minimum Coverage | Enforcement |
|------------|-----------------|-------------|
| Critical path | 80% | CI block on failure |
| High priority | 60% | Warning in CI |
| Medium priority | 40% | Reported only |
| Low priority | 20% | Reported only |

### Test Result Reporting

- **PR Comments**: Automated test results with coverage delta
- **Slack/Email**: Notifications for test failures
- **Dashboard**: Coverage trends over time
- **Regression Reports**: New test failures highlighted

### Performance Regression Detection

```yaml
performance-benchmarks:
  runs-on: ubuntu-latest
  steps:
    - name: Run benchmarks
      run: go test -bench=. -benchmem ./... > bench.txt
    - name: Compare with baseline
      run: go install golang.org/x/perf/cmd/benchstat@latest && benchstat old.txt new.txt
```

## Best Practices

### DO:

1. **Write tests before fixing bugs** - This ensures you understand the issue
2. **Use table-driven tests** - For multiple test cases with similar structure
3. **Test error paths** - Not just happy paths
4. **Use test helpers** - Reduce duplication in test setup
5. **Keep tests independent** - No test should depend on another
6. **Use descriptive names** - Test names should describe what they test
7. **Add comments for complex tests** - Explain the "why", not the "what"
8. **Run tests locally** - Before pushing to CI
9. **Use race detector** - For concurrent code
10. **Keep tests fast** - Slow tests don't get run

### DON'T:

1. **Don't test implementation details** - Test behavior, not internals
2. **Don't use sleep for synchronization** - Use channels or other proper sync mechanisms
3. **Don't ignore flaky tests** - Fix them or mark them as flaky
4. **Don't write catch-all tests** - Be specific about what you're testing
5. **Don't mock everything** - Use real components for integration tests
6. **Only skip for explicit environment preconditions** - Keep product-logic tests strict; use skips only when required dependencies (local stack, socket permissions) are absent
7. **Don't test third-party code** - Trust your dependencies
8. **Don't over-mock** - It makes tests brittle
9. **Don't test getters/setters** - Unless they have logic
10. **Don't write tests that are too complex** - If test is complex, refactor code

## Additional Resources

- [Effective Go: Testing](https://go.dev/doc/effective_go#testing)
- [Go Wiki: Table Driven Tests](https://github.com/golang/go/wiki/TableDrivenTests)
- [Testing Guidelines](https://github.com/golang/go/wiki/Testing)
- [Testify Documentation](https://github.com/stretchr/testify)
- [React Testing Library](https://testing-library.com/react)

## Changelog

- 2026-02-21: Initial testing strategy document created
