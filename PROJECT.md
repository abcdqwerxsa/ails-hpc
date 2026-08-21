# Project: AILS-HPC QOS Management System

## Architecture
AILS-HPC is a cloud-native HPC computing management platform integrating Golang (Gin, SQLite, Docker/Slurm CLI, slurmrestd) and React 18 (TypeScript, Vite, TanStack Router).
The QOS management system establishes a multi-tier quota and priority governance pipeline:
1. **SlurmDBD / SACCTMGR Core**: Single source of truth for QOS definitions and User Associations.
2. **Backend Domain Services**:
   - `pkg/services/admin`: QOS CRUD, User Association QOS, Tenant QOS binding, Audit logging.
   - `pkg/services/jobs`: Job submission with `--qos` CLI/REST injection.
   - `pkg/services/containers`: Web-IDE container session launch with `--qos` injection.
   - `cmd/apiserver`: REST API routes, RBAC permission gating (`qos:manage`, `tenant:users:manage`, `jobs:submit`).
3. **Frontend Presentation Layer**:
   - `apps/web/app/services/slurm.ts`: Typed API client & data models.
   - `apps/web/app/components/scheduler_sections.tsx`: Scheduler QOS CRUD & list management.
   - `apps/web/app/routes/admin.tsx`: User Association & Tenant QOS management.
   - `apps/web/app/routes/jobs.tsx`: Job submission with QOS selection & policy hints.
   - `apps/web/app/routes/webide.tsx`: Web-IDE launch with QOS selection.

## Feature Inventory
| # | Feature | Description | Milestone | Source |
|---|---------|-------------|-----------|--------|
| F1 | QOS Extended Model & Parsing | Extended QOS struct (GrpTRES, MaxTRESPerUser, MaxJobsPerUser, MaxSubmitJobsPerUser, MaxWallDuration, Priority, Description) with robust sacctmgr parsing | M1 | ORIGINAL_REQUEST §R1 |
| F2 | QOS CRUD & Audit API | Complete Create, Modify, Delete, and List QOS endpoints with whitelist regex validation and audit logging | M1 | ORIGINAL_REQUEST §R1 |
| F3 | User Association & Tenant QOS | sacctmgr modify user/account association QOS, tenant admin scope checking, available QOS endpoint | M2 | ORIGINAL_REQUEST §R2 |
| F4 | Job Submission QOS Passthrough | SubmitJobRequest QOS field, validation, CLI --qos argument injection, REST Job.Qos passthrough | M3 | ORIGINAL_REQUEST §R3 |
| F5 | Web-IDE Session QOS Passthrough | ContainerLaunchRequest QOS field, IDE batch script & CLI/REST submission --qos injection | M3 | ORIGINAL_REQUEST §R3 |
| F6 | Frontend Type Definitions & API | TypeScript interfaces (QOSInfo, CreateQOSRequest, UpdateQOSRequest, etc.) and slurm.ts API client methods | M4 | ORIGINAL_REQUEST §R4 |
| F7 | Scheduler QOS Management UI | Full QOS table, create modal, edit modal, delete confirmation, and tenant binding UI | M4 | ORIGINAL_REQUEST §R4 |
| F8 | User & Tenant Admin QOS UI | Tenant member list and platform user directory QOS column, User QOS assignment modal/selector | M4 | ORIGINAL_REQUEST §R4 |
| F9 | Job & Web-IDE QOS Selection UI | QOS dropdown selectors with quota/time/priority policy tips and client validation | M4 | ORIGINAL_REQUEST §R4 |
| F10 | E2E Integration & Verification | Slurm enforcement verification, live QOS limit tests, unit & E2E full test suite passing | M5 | ORIGINAL_REQUEST §Acceptance Criteria |

## Milestones
| # | Name | Scope | Dependencies | Status | Key Outputs |
|---|------|-------|-------------|--------|-------------|
| 1 | Backend QOS Model & CRUD API | F1, F2: Domain model, SACCTMGR CRUD, router endpoints, validation, audit logs, unit tests | none | DONE | `cluster_admin.go`, `handler.go`, `router.go`, `cluster_admin_test.go` (100% tests passing) |
| 2 | User Association & Tenant QOS Governance | F3: User association QOS API, tenant admin scoping, getAvailableQOS API, unit tests | M1 | DONE | `cluster_admin.go`, `handler.go`, `router.go`, unit/adversarial tests (100% passing) |
| 3 | Job & Web-IDE QOS Injection | F4, F5: Job submission & Web-IDE container launch QOS passthrough, sbatch injection, unit tests | M1 | DONE | `jobs`, `containers`, `slurmrest`, unit/adversarial tests (100% passing) |
| 4 | Frontend QOS Visual Governance & Forms | F6, F7, F8, F9: TypeScript API client, Scheduler QOS CRUD UI, Admin User QOS UI, Job & IDE QOS selectors | M2, M3 | PLANNED | Planned next |
| 5 | E2E Integration & Coverage Hardening | F10: Tiers 1-4 E2E verification, Slurm limits enforcement, Tier 5 adversarial hardening | M4 | PLANNED | Planned |

## Interface Contracts
### Admin Service ↔ Slurm CLI (SACCTMGR)
- `ListQOS()` -> `sacctmgr -nP show qos format=name,priority,grptres,maxtrespu,maxwallpu,maxjobspu,maxsubmitjobspu || sacctmgr -nP show qos format=name,priority,grptres,maxtres,maxwall,grpj`
- `CreateQOS(name, updates)` -> `sacctmgr -i add qos <name> [Priority=...] [GrpTRES=...] [MaxTRESPerUser=...] [MaxJobsPerUser=...] [MaxSubmitJobsPerUser=...] [MaxWallDuration=...] [Description=...]`
- `UpdateQOS(name, updates)` -> `sacctmgr -i modify qos <name> set [Priority=...] [GrpTRES=...] [MaxTRESPerUser=...] [MaxJobsPerUser=...] [MaxSubmitJobsPerUser=...] [MaxWallDuration=...] [Description=...]`
- `DeleteQOS(name)` -> `sacctmgr -i delete qos <name>`
- `SetUserQOS(username, tenantSlug, defaultQOS, allowedQOS)` -> `sacctmgr -i modify user <clusterUser> account=<parentAccount> set qos=<allowedQOS> defaultqos=<defaultQOS>`
- `GetUserQOS(username)` -> `sacctmgr -nP show assoc where user=<clusterUser> format=User,Account,QOS,DefQOS`

### Jobs / Containers Service ↔ Slurm Submission
- CLI sbatch: `sudo -u <clusterUser> sbatch ... [--qos=<qos>] ...`
- REST slurmrestd: `slurmReq.Job.Qos = req.QOS`

### Frontend ↔ Backend API Endpoints
- `GET /api/v1/admin/qos`: List all QOS (Admin only)
- `POST /api/v1/admin/qos`: Create QOS (Admin only)
- `PATCH /api/v1/admin/qos/:name`: Modify QOS (Admin only)
- `DELETE /api/v1/admin/qos/:name`: Delete QOS (Admin only)
- `GET /api/v1/admin/users/:username/qos`: Get user association QOS (Admin)
- `PATCH /api/v1/admin/users/:username/qos`: Set user association QOS (Admin)
- `PATCH /api/v1/tenants/me/users/:username/qos`: Set user association QOS (Tenant Admin)
- `GET /api/v1/slurm/qos/available`: Get current user allowed QOS list & default QOS
- `POST /api/v1/slurm/jobs/submit`: Submit job with optional `qos`
- `POST /api/v1/slurm/containers/launch`: Launch Web-IDE session with optional `qos`

## Code Layout
- `pkg/services/admin/cluster_admin.go`: Backend QOS domain model, SACCTMGR CRUD methods, User Association methods.
- `pkg/services/admin/handler.go`: HTTP handler for QOS and User Association endpoints.
- `pkg/services/admin/service.go`: Provisioner interface and tenant QOS management.
- `pkg/services/jobs/types.go`, `service.go`, `handler.go`: Job submission types and CLI/REST submission logic.
- `pkg/services/containers/types.go`, `service.go`, `handler.go`: Container launch types and IDE session script generation.
- `pkg/slurmrest/slurm_client.go`: REST client models for slurmrestd.
- `cmd/apiserver/router.go`: API route registration and RBAC permission binding.
- `apps/web/app/services/slurm.ts`: Frontend TypeScript types and API client.
- `apps/web/app/components/scheduler_sections.tsx`: Frontend QOS management panel.
- `apps/web/app/routes/admin.tsx`: Frontend user and tenant QOS management.
- `apps/web/app/routes/jobs.tsx`: Frontend job submission with QOS selection.
- `apps/web/app/routes/webide.tsx`: Frontend Web-IDE launch with QOS selection.
