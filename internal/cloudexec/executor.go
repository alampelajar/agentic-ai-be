package cloudexec

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"agentic-ai-backend/internal/jobspec"
)

// MaxOutputBytes bounds what is kept of each of stdout and stderr.
const MaxOutputBytes = 64 << 10

// RunRequest is everything an executor needs. Name is the container name the job
// can be killed by.
type RunRequest struct {
	Name     string
	FilesDir string
	Spec     jobspec.CommandSpec
	Timeout  time.Duration
}

// RunResult is what came back. Exactly one of TimedOut and Cancelled is true when
// the run was cut short; otherwise ExitCode is the command's own exit code.
type RunResult struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	TimedOut  bool
	Cancelled bool
	Truncated bool
}

// Executor runs one allowlisted command somewhere isolated. The worker depends on
// this interface only, so a different sandbox (gVisor, Firecracker, a managed
// service) can replace Docker without touching the business logic.
//
// Run must stop the command when ctx is cancelled (Cancelled) or when Timeout
// passes (TimedOut), and must have removed whatever it created by the time it
// returns. An error means the command could not be started or supervised.
type Executor interface {
	Name() string
	// Available says whether the sandbox can run jobs right now.
	Available(ctx context.Context) error
	Run(ctx context.Context, request RunRequest) (RunResult, error)
}

// ErrDisabled is returned by the executor used when execution is not turned on.
var ErrDisabled = errors.New("code execution is not enabled on this server")

// DisabledExecutor refuses everything. It is the safe default.
type DisabledExecutor struct{}

func (DisabledExecutor) Name() string                    { return "disabled" }
func (DisabledExecutor) Available(context.Context) error { return ErrDisabled }
func (DisabledExecutor) Run(context.Context, RunRequest) (RunResult, error) {
	return RunResult{}, ErrDisabled
}

// limitedBuffer keeps the first MaxOutputBytes written to it and drops the rest
// while still accepting writes, so a chatty process is never blocked by it.
type limitedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	remaining := MaxOutputBytes - l.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			l.buffer.Write(p[:remaining])
			l.truncated = true
		} else {
			l.buffer.Write(p)
		}
	} else if len(p) > 0 {
		l.truncated = true
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

func (l *limitedBuffer) Truncated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.truncated
}
