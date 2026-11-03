# Phase 6: Cloud Workspace and Execution

## 1. Audit (Stage A)

What exists today:

| Area | Finding |
|---|---|
| Backend | Go, Gin, GORM, PostgreSQL. `AutoMigrate` in `config/database.go`. |
| Deployment | No Dockerfile, compose file or hosting manifest in the repository. The only CI is `go-ci.yml` (build and test). Hosting is therefore not defined by the code. |
| Docker | The CLI is installed on the development machine but the daemon was not running during the audit, so no container could be started. Sandbox behaviour could not be verified end to end here. |
| File and command access today | Only through the Local Agent Runner (`internal/localrunner`, port 8765 on the user's computer): `list-files`, `read-file`, `write-file`, `git-status`, `go-test`, `go-build`, `npm-*`, `python-test`. Its Docker mode already uses `--network none`, `--read-only`, `--pids-limit`, memory and CPU limits and `no-new-privileges`. |
| Tool Registry | `services.ToolRegistry` holds `list-files`, `read-file` and the `desktop.*` tools. Skill manifests may name `workspace.list/read/write/execute`, but those names granted nothing. |
| Frontend tool loop | `src/features/ai/index.tsx` parses `<LOCAL_AGENT_TOOL>` (runner) and `<AGENT_TOOL>` (desktop) markers, asks the user for approval, then calls the runner. |
| `AgentRun` / `AgentRunStep` | **Do not exist.** Phase 5B shipped `AgentDelegation`. `ExecutionJob.AgentRunID` is therefore a plain nullable number with no foreign key, ready for a later phase. |
| `Task` | Has no workspace. Phase 6 adds a nullable `workspace_id`. |

## 2. Architecture

```
Browser ──> API server (Gin) ──> PostgreSQL (metadata, jobs, approvals)
                 │
                 ├── Workspace service ──> WorkspaceStorage (files)
                 │
                 └── creates ExecutionJob (status queued)
                                   │
Execution worker (separate binary) ┘  claims jobs, runs them through an Executor
        └── Executor ──> DockerExecutor (hardened container) | DisabledExecutor
```

- **API server** authenticates, checks ownership, does CRUD and file operations
  and queues jobs. It never runs project code. Nothing in the API process calls
  `exec` on user content.
- **Workspace service** (`internal/cloudworkspace`) owns safe paths, the file
  tree, reading, writing, ZIP import and export, change detection and quotas.
- **Execution worker** (`cmd/execution-worker`) is a separate process. It claims
  queued jobs from PostgreSQL with `FOR UPDATE SKIP LOCKED`, runs them through the
  `Executor` interface, enforces the timeout, watches for cancellation, stores
  bounded output and cleans up.
- **Executor** is an interface. `DockerExecutor` builds the container command;
  `DisabledExecutor` refuses every job. A sandbox provider (gVisor, Firecracker,
  a managed service) can replace the Docker one without changing business logic.

### Storage
`WorkspaceStorage` is an interface. `LocalDevelopmentStorage` keeps each workspace
under `<root>/<user id>/<workspace uuid>/` with two folders: `files/` (the
project, the only part users and containers can reach) and `meta/` (baseline
manifest for change detection). An object storage adapter is the intended
production implementation; it is an interface only in this phase. File content is
never stored in PostgreSQL.

### Isolation
Every path is resolved relative to the workspace root; absolute paths, `..`,
symlinks and anything that would leave the root are refused. Workspaces of
different users live in different directories and the storage only hands out the
directory for the owner's workspace id.

## 3. Execution safety

Execution is **off by default** (`CLOUD_EXECUTION_ENABLED` unset). The worker
turns it on only when the Docker daemon answers and the approved images are
present. Without that, jobs fail with a clear message and workspaces stay fully
usable in read/write mode.

Every container is started with: non-root user, `--network none`,
`--read-only` root filesystem, `--cap-drop ALL`, `--security-opt no-new-privileges`,
no `--privileged`, no Docker socket, CPU, memory and PID limits, a size-limited
tmpfs for `/tmp`, a timeout enforced by the worker (which also kills the
container by name), and only the workspace `files/` directory mounted. The `meta/`
directory, `.env` files and the backend's environment are not visible to it.

Commands come from a backend allowlist (`go-test`, `go-build`, `npm-test`,
`npm-build`, `npm-lint`, `python-test`) and are passed as an argument array;
there is no shell and no free-form command. Project tests run code written by the
user, which is why they may only run in the sandbox.

### Network policy and dependencies
The network is off. `npm install` and `pip install` therefore do not work inside a
job. The planned answer is a read-only dependency cache volume prepared outside the
job, and later an approved network policy limited to package registries. Neither
is built in this phase; projects must already contain what they need (vendored Go
modules, committed `node_modules`) for tests to run.

## 4. Approval
Reading and listing run directly. Writing a file and running a command need an
explicit approval:

1. The browser asks the API for the tool (`POST /api/workspace-tools/run`).
2. For a mutating tool the API stores a pending approval bound to the user, the
   workspace, the tool and a hash of the arguments, and answers `approval_required`.
3. Only the signed-in user can approve it (`POST /api/workspace-tools/approvals/:id/approve`).
   The model cannot, because approval is a separate authenticated request that the
   model's text never triggers. Approvals are single use and expire after 60 seconds.
4. The tool then runs with exactly the approved arguments.

Every tool call is recorded in Tool Activity with redaction.

## 5. Known limits
- The sandbox could not be exercised against a real Docker daemon in the
  development environment; its container arguments are covered by unit tests and
  the integration tests skip when no daemon is available.
- The local storage implementation is a single-node design. A multi-node
  deployment needs the object storage adapter or a shared volume.
- Private GitHub repositories are not supported (no OAuth or GitHub App yet).
- No dependency installation inside jobs (see above).

## 6. Reference

### API
| Method and path | Purpose |
|---|---|
| `GET/POST /api/workspaces` | list; create (JSON for empty or `github`, multipart for a ZIP) |
| `GET/PATCH/DELETE /api/workspaces/:id` | read, rename or set the default runtime, delete |
| `GET /api/workspaces/:id/tree?path=` | file tree (bounded, symlinks left out, secret files flagged) |
| `GET/PUT /api/workspaces/:id/file` | read or write one text file |
| `GET /api/workspaces/:id/changes` | added, modified and deleted files since creation |
| `POST /api/workspaces/:id/export` | download the project as a ZIP (secret files left out) |
| `GET/POST /api/workspaces/:id/executions` | list or queue a job (a job type only) |
| `GET /api/executions/:id`, `POST /api/executions/:id/cancel` | read or cancel a job |
| `POST /api/workspace-tools/run` | run an agent tool; a mutating tool returns a pending approval |
| `POST /api/workspace-tools/approvals/:id/approve` or `/deny` | the user's decision |

Chat accepts `workspace_id`; tasks carry a nullable `workspace_id`.

### Tools (Tool Registry)
`workspace.list_files`, `workspace.read_file`, `workspace.write_file`,
`workspace.get_changes`, `workspace.run_test`, `workspace.build`,
`workspace.get_execution`. Roles: Coding gets all; Testing gets read, test, build;
Debugging gets read and test; Review, Planning and unknown agents only read.
`list-files`, `read-file` and the `desktop.*` tools are unchanged, and the old
manifest names `workspace.list/read/write/execute` are still accepted in skills.

### Settings (environment variables, all optional)
`CLOUD_WORKSPACE_ROOT`, `CLOUD_EXECUTION_ENABLED` (exactly `true`),
`CLOUD_EXEC_IMAGE_GO/NODE/PYTHON`, `CLOUD_EXEC_MEMORY`, `CLOUD_EXEC_CPUS`,
`CLOUD_EXEC_PIDS_LIMIT`, `CLOUD_EXEC_RUN_AS`, `CLOUD_EXEC_MAX_CONCURRENT`,
`CLOUD_EXEC_MAX_PER_USER`. They are listed, commented out, in `.env.example`.

Built-in limits (`cloudworkspace.DefaultLimits`): 1 MiB read and 2 MiB write per
file, 5,000 tree entries, 50 MiB archive, 200 MiB expanded, 10,000 files per
archive, 500 MiB per user, 10 workspaces per user; 64 KiB of output per stream;
3 active jobs per user and 100 queued overall; 60 second approvals, 20 pending.

### Running the pieces
```
go run .                      # API server (queues jobs, never runs them)
go run ./cmd/execution-worker # separate process; needs Docker and the approved images
go run ./cmd/workspace-janitor [-days 90] [-delete]  # lifecycle: list, or remove, unused workspaces
```
The worker refuses to start unless `CLOUD_EXECUTION_ENABLED=true`, the Docker
daemon answers and the three approved images are already pulled.

### What a deployment must provide
- A host or cluster where the worker can reach a container runtime, and images
  pulled in advance (jobs have no network, so they cannot pull).
- Storage shared by the API and the worker (a volume or, later, the object storage
  adapter), readable and writable by `CLOUD_EXEC_RUN_AS`.
- Preferably a stronger boundary than plain containers around the worker (gVisor,
  Kata, Firecracker or a dedicated VM), and egress firewalling on the worker host.

### Lifecycle
Workspaces are never deleted automatically. `workspace-janitor` lists workspaces
not opened for the retention period (default 90 days, never less than 7) that are
ready and have no active job, and deletes them only with `-delete`.

### Not done in this phase
- The sandbox was not exercised against a real Docker daemon here. Its arguments
  and process handling are tested with a stand-in CLI; run the worker against a
  real daemon before enabling execution anywhere.
- Dependency installation and an approved network policy for package registries.
- Object storage adapter, private GitHub repositories (OAuth or a GitHub App), a
  diff against the original file content (only change status is kept), streaming
  output (the console polls), and `AgentRun`/`AgentRunStep` (they do not exist yet).
