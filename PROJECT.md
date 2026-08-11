# Project: Slurm Modular Monolith Full-Stack Development

## Architecture
- **Modular Monolith Architecture**: Core business domain services are decoupled into package modules under `pkg/services/`:
  - `pkg/services/jobs`: Job submission (`sbatch`), job control (`scancel`, `shold`, `srequeue`), SlurmRESTd integration
  - `pkg/services/nodes`: Node state management (`DRAIN` / `RESUME`), node grid status monitoring
  - `pkg/services/containers`: Web-VSCode & JupyterLab container workspaces, JWT auth proxy, quota & lifecycle recycling
  - `pkg/services/billing`: Slurm SACCT / Account resource usage statistics, cost reporting & export
  - `apps/web`: Neumorphic 1:1 UI frontend for all modules
  - `cmd/apiserver`: Modular Monolith HTTP API server entrypoint

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| 1 | SDD & Specs | OpenAPI 3.0 specs, mock SlurmRESTd & test helpers | None | DONE |
| 2 | Jobs Service | pkg/services/jobs (sbatch, scancel, shold, srequeue) & UI | M1 | DONE |
| 3 | Nodes Service | pkg/services/nodes (DRAIN/RESUME node control) & UI | M1 | DONE |
| 4 | Container Service | pkg/services/containers (Web-VSCode/Jupyter, JWT proxy) & UI | M1 | DONE |
| 5 | Billing Service | pkg/services/billing (/api/v1/slurm/billing/usage) & UI | M1 | DONE |
| 6 | System Integration & Deployment | Monolith wiring in cmd/apiserver, server 192.168.20.226:8090 deployment | M2, M3, M4, M5 | DONE |
| 7 | Final Acceptance & Hardening | Pass 100% E2E tests (Tiers 1-4) & Adversarial hardening (Tier 5) | M6, TEST_READY | DONE |

## Interface Contracts

### pkg/services/jobs ↔ SlurmRESTd / Web UI
- `POST /api/v1/slurm/jobs/submit` -> sbatch job submission -> returns valid JobID
- `POST /api/v1/slurm/jobs/:id/cancel` -> scancel job termination
- `POST /api/v1/slurm/jobs/:id/hold` -> shold job pause
- `POST /api/v1/slurm/jobs/:id/requeue` -> srequeue job restart

### pkg/services/nodes ↔ SlurmRESTd / Web UI
- `GET /api/v1/slurm/nodes` -> node list & status
- `POST /api/v1/slurm/nodes/:name/state` -> scontrol update NodeName=... State=DRAIN/RESUME

### pkg/services/containers ↔ Web-VSCode / JupyterLab / Web UI
- `POST /api/v1/slurm/containers/launch` -> returns JWT authenticated proxy URL
- `GET /api/v1/slurm/containers/list` -> active workspace container instances
- `DELETE /api/v1/slurm/containers/:id` -> terminate/recycle container instance

### pkg/services/billing ↔ SACCT / Web UI
- `GET /api/v1/slurm/billing/usage` -> CPU/Memory/GPU usage hours breakdown
- `GET /api/v1/slurm/billing/export` -> JSON / Chart export report data

## Code Layout
- Backend:
  - `pkg/services/jobs/*.go`, `pkg/services/jobs/*_test.go`
  - `pkg/services/nodes/*.go`, `pkg/services/nodes/*_test.go`
  - `pkg/services/containers/*.go`, `pkg/services/containers/*_test.go`
  - `pkg/services/billing/*.go`, `pkg/services/billing/*_test.go`
  - `pkg/services/spec/*.yaml` (OpenAPI 3.0 specs)
  - `cmd/apiserver/main.go`
- Frontend:
  - `apps/web/index.html`
  - `apps/web/js/app.js`
  - `apps/web/css/neumorphism.css`
