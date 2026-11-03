# Phase 6: Cloud Workspace and Execution

## 1. Audit (Stage A)

What exists today:

| Area | Finding |
|---|---|
| Backend | Go, Gin, GORM, PostgreSQL. `AutoMigrate` in `config/database.go`. |
| Deployment | No Dockerfile, compose file or hosting manifest in the repository. The only CI is `go-ci.yml` (build and test). Hosting is therefore not defined by the code. |
| Docker | The daemon was not running during the original audit, so the sandbox was first written against a stand-in CLI. It has since been exercised on a real daemon (Docker Desktop 29.8.1, linux/amd64); see section 7 for what was proven and what was found. |
| File and command access before Phase 6 | Only through the Local Agent Runner (`internal/localrunner`, port 8765 on the user's computer): `list-files`, `read-file`, `write-file`, `git-status`, `go-test`, `go-build`, `npm-*`, `python-test`. Its Docker mode already uses `--network none`, `--read-only`, `--pids-limit`, memory and CPU limits and `no-new-privileges`. |
| Tool Registry | `services.ToolRegistry` holds `list-files`, `read-file` and the `desktop.*` tools. Skill manifests may name `workspace.list/read/write/execute`, but those names granted nothing. |
| Frontend tool loop | `src/features/ai/index.tsx` parses `<LOCAL_AGENT_TOOL>` (runner) and `<AGENT_TOOL>` (desktop) markers, asks the user for approval, then calls the runner. |
| `AgentRun` / `AgentRunStep` | **Do not exist.** Phase 5B shipped `AgentDelegation`. `ExecutionJob.AgentRunID` is therefore a plain nullable number with no foreign key, ready for a later phase. |
| `Task` | Has no workspace. Phase 6 adds a nullable `workspace_id`. |

Since Phase 6 the Local Agent Runner is **optional**. Workspaces, file editing, tests and builds
need only the browser and the server. The runner stays for desktop control and is managed under
Settings -> Integrations -> Desktop Runtime (see `local-agent-runner.md`).

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

Every container is started with: non-root user, `--network none` (only dependency
install jobs differ, see below),
`--read-only` root filesystem, `--cap-drop ALL`, `--security-opt no-new-privileges`,
no `--privileged`, no Docker socket, CPU, memory and PID limits, a size-limited
tmpfs for `/tmp` (`noexec`; Go jobs also get an executable scratch tmpfs, because
`go test` runs what it builds), a timeout enforced by the worker (which also kills the
container by name), and only the workspace `files/` directory mounted. The `meta/`
directory, `.env` files and the backend's environment are not visible to it.

Commands come from a backend allowlist (`go-test`, `go-build`, `npm-test`,
`npm-build`, `npm-lint`, `python-test`, plus the install jobs `go-deps`,
`npm-install`, `pip-install`) and are passed as an argument array;
there is no shell and no free-form command. Project tests run code written by the
user, which is why they may only run in the sandbox.

### Network policy and dependencies
Test, build and lint jobs have **no network at all** (`--network none`). A project
with dependencies therefore needs one extra step first, a *dependency install job*:

| Job type | Command | Result in the workspace |
|---|---|---|
| `go-deps` | `go mod tidy`, then `go mod vendor` (two fixed steps) | `go.sum` completed, `vendor/` written; the Go test and build jobs build from it (`-mod=vendor`) |
| `npm-install` | `npm install --ignore-scripts --no-audit --no-fund --registry=https://registry.npmjs.org/` | `node_modules/` |
| `pip-install` | `python -m pip install --only-binary=:all: --target /workspace/.pydeps pytest [-r requirements.txt]` | `.pydeps/`, put on `PYTHONPATH` for `python-test` |

Install jobs are the only jobs that get a network, and it is not the internet:

```
install container --- cloudexec-install (docker network --internal) ---> registry proxy ---> npm, Go proxy, PyPI
 (read-only, no caps,   no route to the host, the LAN or                  allowlisted names,
  non-root, same        the internet; DNS does not resolve                port 443 only,
  limits)                                                                 public IPs only
```

- **No project code runs during an install.** npm scripts are off by the command-line
  flag (a project `.npmrc` cannot turn them back on; tested), pip only accepts wheels
  (no `setup.py`), and `go mod` never runs code. What is installed runs later, in the
  network-less sandbox.
- **The proxy is the only way out** (`cmd/registry-proxy`, `internal/registryproxy`). It
  accepts `CONNECT` only, to five names (`registry.npmjs.org`, `proxy.golang.org`,
  `sum.golang.org`, `pypi.org`, `files.pythonhosted.org`), port 443, and resolves DNS
  itself: a name that points to a private, loopback or link-local address (cloud
  metadata included) is refused, and it dials the address it checked. A dependency that
  points at any other server (`https://example.com/x.tgz`, a GitHub tarball, an
  address literal) fails; this is the SSRF barrier for installs.
- **The worker checks before it runs an install** that the network exists, is
  `--internal` (a network with internet access would be refused, not trusted) and that
  the proxy container is up. Otherwise the job fails with the reason, and nothing starts.
- **Off by default.** Setting up the network is a deliberate step
  (`bash deploy/registry-proxy/setup.sh`, see `deploy/registry-proxy/README.md`), and the
  API server and the worker both need `CLOUD_EXEC_INSTALL_NETWORK`. Without it install
  jobs are refused with a clear message and test/build still work for projects that
  need no external packages.

Installed folders (`node_modules`, `.pydeps`, at any depth) are *generated*: they are
listed as one closed entry, are not readable or writable through the file API or the
agent tools, are not extracted from uploaded archives, are left out of the change list
and the export, and do not count against the per-user project quota. They have their
own limit instead (300 MiB, checked while the job runs and after it; see Known limits).
`vendor/` stays an ordinary visible folder.

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
- The sandbox has been exercised on one real host only: Docker Desktop on Windows
  (WSL2 backend). A Linux host has not been tried here, and file ownership is the
  main thing that can differ (see the deployment list below).
- Docker cannot put a disk quota on a bind mount. The worker measures the workspace
  every few seconds while a job runs and stops a job that takes it past
  `CLOUD_EXEC_MAX_WORKSPACE_MB` (1 GiB) or its dependencies past
  `CLOUD_EXEC_MAX_DEPENDENCY_MB` (300 MiB); a fast writer can still overshoot between
  two checks. Put `CLOUD_WORKSPACE_ROOT` on a filesystem with real quotas in production.
- Install limits: `npm install` runs without scripts, so packages that need a build step
  on install (native add-ons without a prebuilt binary) do not work; pip only installs
  wheels; only `requirements.txt` is read for Python (not `pyproject.toml`
  dependencies); no private registries, `GOPRIVATE` or git dependencies; the Go
  version is the image's (`GOTOOLCHAIN=local`), so a `go.mod` that needs a newer Go fails.
- The local storage implementation is a single-node design. A multi-node
  deployment needs the object storage adapter or a shared volume.
- Private GitHub repositories are not supported (no OAuth or GitHub App yet).
- Dependencies are installed per workspace and kept inside it; nothing is shared or
  cached between workspaces, so each install downloads again.

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
`workspace.install_dependencies`, `workspace.get_execution`. Roles: Coding gets all;
Testing gets read, test, build and install; Debugging gets read and test; Review,
Planning and unknown agents only read. `install_dependencies` needs the user's
approval like every tool that runs something.
`list-files`, `read-file` and the `desktop.*` tools are unchanged, and the old
manifest names `workspace.list/read/write/execute` are still accepted in skills.

### Settings (environment variables, all optional)
`CLOUD_WORKSPACE_ROOT`, `CLOUD_EXECUTION_ENABLED` (exactly `true`),
`CLOUD_EXEC_IMAGE_GO/NODE/PYTHON`, `CLOUD_EXEC_MEMORY`, `CLOUD_EXEC_CPUS`,
`CLOUD_EXEC_PIDS_LIMIT`, `CLOUD_EXEC_RUN_AS`, `CLOUD_EXEC_MAX_CONCURRENT`,
`CLOUD_EXEC_MAX_PER_USER`, `CLOUD_EXEC_INSTALL_NETWORK`, `CLOUD_EXEC_INSTALL_PROXY`,
`CLOUD_EXEC_MAX_DEPENDENCY_MB`, `CLOUD_EXEC_MAX_WORKSPACE_MB`. They are listed,
commented out, in `.env.example`.

Built-in limits (`cloudworkspace.DefaultLimits`): 1 MiB read and 2 MiB write per
file, 5,000 tree entries, 50 MiB archive, 200 MiB expanded, 10,000 files per
archive, 500 MiB per user, 10 workspaces per user; 64 KiB of output per stream;
3 active jobs per user and 100 queued overall; 60 second approvals, 20 pending.

### Running the pieces
```
go run .                      # API server (queues jobs, never runs them)
go run ./cmd/execution-worker # separate process; needs Docker and the approved images
bash deploy/registry-proxy/setup.sh   # once per host, only if dependency installation is wanted
go run ./cmd/workspace-janitor [-days 90] [-delete]  # lifecycle: list, or remove, unused workspaces
```
The worker refuses to start unless `CLOUD_EXECUTION_ENABLED=true`, the Docker
daemon answers and the three approved images are already pulled.

### What a deployment must provide
- A host or cluster where the worker can reach a container runtime, and images
  pulled in advance (jobs have no network, so they cannot pull).
- Storage shared by the API and the worker (a volume or, later, the object storage
  adapter), readable and writable by `CLOUD_EXEC_RUN_AS`. On a Linux host set
  `CLOUD_EXEC_RUN_AS` to the uid:gid of the account that owns `CLOUD_WORKSPACE_ROOT`
  (the worker's service account), so the files a container creates belong to that
  account and can be deleted by the API and the janitor. The default `65534:65534`
  is only right when the storage is writable by it. Not exercised here (see Known limits).
- For installs: the registry network and proxy from `deploy/registry-proxy/setup.sh`,
  built on the host that runs the worker.
- Preferably a stronger boundary than plain containers around the worker (gVisor,
  Kata, Firecracker or a dedicated VM), and egress firewalling on the worker host.

### Lifecycle
Workspaces are never deleted automatically. `workspace-janitor` lists workspaces
not opened for the retention period (default 90 days, never less than 7) that are
ready and have no active job, and deletes them only with `-delete`.

### Not done in this phase
- The sandbox has not been run on a Linux host, under a different Docker setup
  (rootless, Podman) or with gVisor/Firecracker.
- A shared dependency cache across workspaces, private registries and git dependencies.
- Object storage adapter, private GitHub repositories (OAuth or a GitHub App), a
  diff against the original file content (only change status is kept), streaming
  output (the console polls), and `AgentRun`/`AgentRunStep` (they do not exist yet).

## 7. Validation on a real Docker daemon

Everything in this section was run, not assumed. Host: Docker Desktop 29.8.1
(linux/amd64, WSL2 backend), 8 CPUs, about 3.9 GB of memory, on Windows 10, with the
three approved images (`golang:1.27`, `node:22-bookworm`, `python:3.12-slim`).
The tests are skipped unless asked for, so `go test ./...` stays hermetic.

```
# one-time: the registry network and proxy (only needed for the install tests)
CLOUD_EXEC_INSTALL_NETWORK=cloudexec-install-test bash deploy/registry-proxy/setup.sh

CLOUDEXEC_DOCKER_TESTS=1 CLOUDEXEC_INSTALL_NETWORK=cloudexec-install-test \
SKILLS_TEST_DATABASE_URL=postgres://... \
  go test ./internal/cloudexec/ -run 'TestReal' -v -timeout 25m
```

**Proven on the real daemon**

| What | How it was checked |
|---|---|
| A passing, a failing and a broken-build Go project end as `completed` / `failed` with the compiler or test output | `TestRealGoTestPassesAndFails` |
| The container runs as a non-root user, has no capabilities and `no_new_privs`, cannot write the root filesystem (`/etc`, `/usr`, `/`, `/root`), can write `/tmp` and `/workspace`, has no Docker socket or host mount, has none of the server's environment variables, cannot reach the internet or resolve DNS | `TestRealSandboxIsolation` runs a probe as a Go test inside the container and fails on any weakness |
| The PID limit stops a fork bomb | same probe |
| A 256 MiB memory limit stops a test that needs 2 GiB | `TestRealMemoryLimit` |
| Output is cut at 64 KiB and marked truncated | `TestRealOutputIsBounded` |
| A job past its timeout is stopped, and a cancelled job is stopped, in both cases the container is gone afterwards | `TestRealTimeoutKillsTheContainer`, `TestRealCancelKillsTheContainer` |
| The same, through the real worker and a real PostgreSQL row: queue, claim, run, result, timeout, cancel while running, no container left | `TestRealWorker*` |
| npm, pip and Go dependencies install and then the **offline** test passes (and fails before the install) | `TestRealNpmInstallThenOfflineTest`, `TestRealPipInstallThenOfflineTest`, `TestRealGoDepsVendorThenOfflineTest` |
| No npm script runs during an install: `preinstall`, `install`, `postinstall`, `prepare` of the project and of a dependency, with a project `.npmrc` that says `ignore-scripts=false`. Removing `--ignore-scripts` makes the test fail (all five scripts then run), so the test is not vacuous | `TestRealNpmInstallRunsNoProjectScripts` |
| A dependency that points at `example.com`, the metadata address or a GitHub tarball fails, and the proxy log shows the refusal | `TestRealInstallCannotReachAnythingButTheRegistries` |
| From inside the install network: direct internet, a public DNS server and public DNS all fail; the proxy answers 200 for the registry and refuses `example.com`, `github.com`, ports 80, 22 and 5432, the metadata address and loopback | `TestRealInstallNetworkHasNoWayOut` |
| The full path: `npm-install` job, then `npm-test` job, through the worker; the workspace's recorded size does not include `node_modules` | `TestRealWorkerInstallsDependenciesThenTestsOffline` |

**Found by doing this, and fixed**

1. `go test` failed with `fork/exec ... permission denied`: Docker mounts a `--tmpfs`
   `noexec`, and `go test` runs the binary it has just built. Go jobs now get a second
   scratch area (`/gotmp`, `exec`, same size) for the compiler's temporary files and
   build cache; `/tmp` stays `noexec` for every job.
2. `go mod vendor` alone fails without a complete `go.sum`, which is the normal state of
   a project an agent has just written. `go-deps` is therefore `go mod tidy` followed by
   `go mod vendor`, each in its own container inside one timeout, stopping at the first failure.
3. The `handlers` tests opened a database pool per fixture and never closed it, which hit
   PostgreSQL's connection limit once one more fixture was added. They now close it.

**Not covered by any of this**: a Linux host (ownership of files written by the container
user, the bind-mount permission model), rootless Docker, a stronger boundary such as
gVisor, and behaviour under a hostile workload beyond the probes above.
