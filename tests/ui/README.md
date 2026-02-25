# UI Tests (Playwright)

## Setup
```bash
cd tests/ui
npm install
npx playwright install chromium
```

## Run
```bash
# headless
npm test

# headed
npm run test:headed

# debug mode
npm run test:debug
```

## Repository-Level Shortcut
```bash
make test-ui
```

## Execution Model
```mermaid
sequenceDiagram
    participant T as Playwright test
    participant C as Controller/UI
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
