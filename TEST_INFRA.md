# E2E Test Infra: AILS-HPC QOS Management System

## Test Philosophy
- Opaque-box & Requirement-driven: All requirements and acceptance criteria from `ORIGINAL_REQUEST.md` are systematically covered across four tiers.
- High-fidelity integration: Tests exercise Go domain services with injected `ClusterRunner` as well as live Slurm commands.

## Feature Inventory
| # | Feature | Source | Tier 1 | Tier 2 | Tier 3 | Tier 4 |
|---|---------|--------|:------:|:------:|:------:|:------:|
| 1 | QOS Parameter Models (GrpTRES, MaxTRESPerUser, MaxJobs, MaxWall, Priority) | R1 | 5 | 5 | ✓ | ✓ |
| 2 | QOS CRUD & Audit API (Create, Update, Delete, List) | R1 | 5 | 5 | ✓ | ✓ |
| 3 | User Association QOS Binding & Tenant Governance | R2 | 5 | 5 | ✓ | ✓ |
| 4 | Job Submission QOS Injection & Passthrough | R3 | 5 | 5 | ✓ | ✓ |
| 5 | Web-IDE Session QOS Injection & Passthrough | R3 | 5 | 5 | ✓ | ✓ |
| 6 | Frontend UI QOS Forms & Visualization | R4 | 5 | 5 | ✓ | ✓ |

## Test Architecture
- **Go Unit & Integration Test Suite**:
  - `pkg/services/admin/cluster_admin_test.go`
  - `pkg/services/jobs/jobs_test.go`
  - `pkg/services/containers/service_test.go`
  - `cmd/apiserver/router_test.go` / `cmd/apiserver/rbac_matrix_test.go`
- **Frontend TypeScript Compilation & Linting**:
  - `npm run build` (`tsc && vite build`) in `apps/web`
- **Slurm Live Verification**:
  - `sacctmgr show qos` validation
  - `sacctmgr show assoc` validation
  - `sbatch --qos` & `squeue` validation

## Real-World Application Scenarios (Tier 4)
| # | Scenario | Features Exercised | Complexity |
|---|----------|--------------------|------------|
| 1 | Multi-Tenant Lab Student vs Mentor QOS Allocation | F1, F2, F3, F7, F8 | High |
| 2 | High-Priority GPU Burst Job Submission & Slurm Enforcement | F1, F4, F9, F10 | High |
| 3 | Web-IDE Interactive GPU Session QOS Rate-Limiting | F1, F5, F9, F10 | Medium |
| 4 | Concurrency Limit Blocking (MaxJobs=1 saturation test) | F1, F4, F10 | High |
| 5 | QOS Lifecycle: Create -> Assign to User -> Submit Job -> Modify QOS -> Delete QOS | F1, F2, F3, F4 | High |

## Coverage Thresholds
- Tier 1: ≥ 30 unit & isolation test cases
- Tier 2: ≥ 30 boundary, corner-case & anti-injection test cases
- Tier 3: Pairwise feature combinations (User Assoc + Job Submit, Tenant Admin + QOS, etc.)
- Tier 4: ≥ 5 end-to-end realistic application scenarios
