# Slurm Modular Monolith - E2E Testing Infrastructure (`TEST_INFRA.md`)

## 1. Overview & Methodology

This document defines the E2E testing infrastructure, methodology, feature inventory, and architecture for the Slurm Modular Monolith system (`ails-hpc`).

### Methodology: Opaque-Box E2E HTTP Validation
The test suite enforces strict **opaque-box (black-box) testing**. The test runner operates strictly over HTTP network protocols by dispatching requests to the Slurm REST API endpoints defined in `PROJECT.md`.
- No internal internal state manipulation or direct function invocation.
- All validations verify HTTP status codes, JSON payload contracts, JWT proxy authentication links, state transition consistency, and error handling.
- The test harness supports dual execution modes:
  1. **Live Deployment Mode**: Point `APISERVER_URL` (e.g. `http://192.168.20.226:8090`) to execute against a live deployed Modular Monolith instance.
  2. **In-Process Engine Mode**: If `APISERVER_URL` is unset, the harness automatically boots an in-process HTTP engine (`httptest.Server`) mounting full REST API route contracts with dynamic state tracking.

---

## 2. Feature Inventory

The test suite covers the four core requirements specified in `ORIGINAL_REQUEST.md` and `PROJECT.md`:

| Requirement | Domain | Features Covered | Endpoints Tested |
|---|---|---|---|
| **R1. Jobs Control** | `pkg/services/jobs` | `sbatch` submission, `scancel` termination, `shold` pause, `srequeue` restart, queue listing, partition assignment | `POST /api/v1/slurm/jobs/submit`<br>`POST /api/v1/slurm/jobs/:id/cancel`<br>`POST /api/v1/slurm/jobs/:id/hold`<br>`POST /api/v1/slurm/jobs/:id/requeue`<br>`GET /api/v1/slurm/jobs` |
| **R1. Node Control** | `pkg/services/nodes` | Node health listing, `DRAIN` node isolation, `RESUME` node restore, node grid status verification | `GET /api/v1/slurm/nodes`<br>`POST /api/v1/slurm/nodes/:name/state` |
| **R2. Container Workspaces** | `pkg/services/containers` | VSCode container launch, JupyterLab container launch, JWT proxy token generation, active workspace listing, dynamic instance recycling | `POST /api/v1/slurm/containers/launch`<br>`GET /api/v1/slurm/containers/list`<br>`DELETE /api/v1/slurm/containers/:id` |
| **R3. Billing & Audit** | `pkg/services/billing` | Resource usage statistics (CPU/Memory/GPU hours), user/project filters, JSON report export, Chart report export | `GET /api/v1/slurm/billing/usage`<br>`GET /api/v1/slurm/billing/export` |

---

## 3. Test Architecture & Tier Organization

The test suite is structured into 4 distinct verification Tiers in `test/e2e/`:

```
test/e2e/
├── harness.go          # Opaque-box HTTP test client & dynamic in-process server engine
├── runner.go           # Exported test runner functions for CLI & package integration
├── tier1_feature.go    # Tier 1: Feature Coverage (>=5 tests per feature group)
├── tier2_boundary.go   # Tier 2: Boundary & Corner Cases (>=5 tests per feature group)
├── tier3_combination.go# Tier 3: Cross-Feature Pairwise Interactions
├── tier4_scenario.go   # Tier 4: Real-World Multi-Node & High-Load Scenarios
└── e2e_test.go         # Go test entrypoint for package test/e2e
```

### Tier Definitions:
- **Tier 1: Feature Coverage (22 Test Cases)**
  - >=5 tests per feature group across Jobs, Nodes, Containers, and Billing.
  - Verifies standard operational paths and output contracts.
- **Tier 2: Boundary & Corner Cases (20 Test Cases)**
  - >=5 tests per feature group.
  - Exercises empty payloads, non-existent IDs, invalid node names, negative resources, quota overflows, invalid date windows, and malformed query options.
- **Tier 3: Cross-Feature Combinations (5 Test Cases)**
  - Pairwise multi-domain workflows combining Job submission, Node state toggling, Container launching, JWT proxying, and Billing accounting.
- **Tier 4: Real-World Scenarios (3 Test Cases)**
  - Complex end-to-end scenarios including 10-job batch scheduling, high-load container capacity recycling, and comprehensive billing report generation & export audits.

---

## 4. Execution Commands

To run the complete opaque-box E2E test suite:

```bash
# Run via test/e2e package
go test -v ./test/e2e/...

# Run via pkg/services entrypoint
go test -v ./pkg/services/...

# Target a live deployed server
APISERVER_URL="http://192.168.20.226:8090" go test -v ./test/e2e/...
```
