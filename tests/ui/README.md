# UI Tests (Playwright)

## Setup
```bash
cd tests/ui
npm install
npx playwright install chromium
```

## Run
```bash
# preferred: auto-build + auto-start stack + run tests
make test-ui

# run directly against an already-running stack
# headless
npm test

# headed
npm run test:headed

# debug mode
npm run test:debug
```

`make test-ui` now bootstraps a local stack automatically via `scripts/test/ui-smoke.sh`.

## Execution Model
```mermaid
sequenceDiagram
    participant T as Playwright test
    participant S as run-local stack
    participant C as Controller/UI
    T->>S: make test-ui bootstrap
    T->>C: open /ui
    T->>C: call API-backed views
    C-->>T: JSON + rendered UI
    T->>T: assertions + screenshots/artifacts
```

## UI Test Entry Points
```mermaid
flowchart TD
    A[npm test] --> B[playwright test]
    C[npm run test:headed] --> B
    D[npm run test:debug] --> B
    B --> E["test-results/artifacts"]
```

## Artifacts
Playwright outputs under `tests/ui/test-results/` (including HTML reports and artifacts when enabled).
