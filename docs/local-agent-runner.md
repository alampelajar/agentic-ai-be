# Local Agent Runner (Windows + Docker)

The Local Agent Runner is a separate loopback-only service. It is intentionally not mounted into the public Go/Gin API: a hosted backend cannot safely assume it can access a user's laptop filesystem. The React app can call this service directly after the user configures its local bearer token.

## Current capabilities

- Register existing local workspaces with `LOCAL_RUNNER_WORKSPACES_JSON`.
- Clone public GitHub repositories over HTTPS into `LOCAL_RUNNER_GIT_ROOT`.
- List workspace files, read UTF-8 files up to 1 MiB, and create/update files up to 1 MiB.
- Execute a fixed allowlist of native or Docker actions: `git-status`, `go-test`, `go-build`, `npm-test`, `npm-build`, `npm-lint`, and `python-test`.
- Authenticated requests, loopback-only binding, origin allowlist, path traversal and symlink escape checks, 2-minute command timeout, and capped command output.
- Docker actions use resource limits, no network, a read-only container root, and a writable workspace mount.

## Windows setup

Use PowerShell in the backend repository. Set a fresh random token of at least 32 characters. The token is a local secret and must not be committed or sent to any remote API.

```powershell
$env:LOCAL_RUNNER_TOKEN = "replace-with-a-random-secret-at-least-32-characters"
$env:LOCAL_RUNNER_ADDR = "127.0.0.1:8765"
$env:LOCAL_RUNNER_ALLOWED_ORIGINS = "http://localhost:5173,http://127.0.0.1:5173"
$env:LOCAL_RUNNER_GIT_ROOT = "$HOME\AgenticAIWorkspaces"
$env:LOCAL_RUNNER_WORKSPACES_JSON = '[{"id":"conelo-fe","name":"Conelo Frontend","path":"C:\\Users\\ALAM\\Desktop\\agentic-ai-fe"},{"id":"conelo-be","name":"Conelo Backend","path":"C:\\Users\\ALAM\\Desktop\\agentic-ai-be"}]'
go run ./cmd/local-runner
```

Change the sample workspace paths to folders that actually exist on your machine. A workspace root must exist before starting the runner. Git must be installed to clone GitHub repositories; Docker Desktop must be running for Docker execution.

## API

Every endpoint except `GET /health` requires `Authorization: Bearer <LOCAL_RUNNER_TOKEN>`.

- `GET /health`
- `GET /api/workspaces`
- `POST /api/workspaces/clone` with JSON `{"url":"https://github.com/owner/repository"}` (public repositories only)
- `GET /api/workspaces/:id/tree?path=.`
- `GET /api/workspaces/:id/file?path=relative/file.go`
- `PUT /api/workspaces/:id/file` with JSON `{"path":"relative/file.go","content":"...","create":false}`
- `POST /api/workspaces/:id/execute` with JSON `{"action":"go-test","runtime":"native"}` or `{"action":"npm-build","runtime":"docker"}`

The `create` flag must be true to create a new file. Paths are always relative to the selected workspace.

## Security notes

- The service binds to loopback only. Do not expose port 8765 to the LAN or internet.
- The bearer token grants file read/write and execution access to every configured workspace. Keep it private and use a strong random value.
- Only public GitHub HTTPS URLs are accepted for cloning. Private repository authentication is not implemented.
- Native test/build commands execute project scripts and therefore can run arbitrary code from that project. Only run trusted code natively. Docker is a defense-in-depth option, not a perfect security boundary; review Docker settings and project files before executing untrusted repositories.
- Docker execution currently has no network. Projects whose dependencies are not already available in the workspace may need dependencies prepared before running tests.
- This is the runner API milestone. Frontend workspace selection, token setup UI, execution approval prompts, and end-to-end chat/tool orchestration still need to be integrated.
