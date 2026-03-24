package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// pollInterval is how often the supervisor checks Redis for restart requests.
	pollInterval = time.Second

	// sigTermTimeout is how long the supervisor waits for SIGTERM to take
	// effect before sending SIGKILL.
	sigTermTimeout = 15 * time.Second
)

// RunSupervisor manages a child process (os.Args[0] <target>) with automatic
// restart support. It blocks until ctx is cancelled or the child exits without
// requesting a restart.
//
// Restart triggers:
//   - A RestartRequest is found in Redis for the given target.
//   - The child exits with RestartExitCode (75).
//
// Graceful shutdown:
//   - On ctx cancellation, the supervisor sends SIGTERM to the child, waits up
//     to sigTermTimeout, then sends SIGKILL before returning.
func RunSupervisor(ctx context.Context, rdb *redis.Client, target string) error {
	for {
		slog.Info("supervisor: spawning child", "target", target)

		child, err := spawnChild(target)
		if err != nil {
			return fmt.Errorf("supervisor: spawn child: %w", err)
		}

		exitCode, err := waitChild(ctx, rdb, target, child)
		if err != nil {
			// ctx was cancelled — caller handles the exit.
			return fmt.Errorf("supervisor: %w", err)
		}

		switch exitCode {
		case RestartExitCode:
			slog.Info("supervisor: child requested restart via exit code", "target", target)
			continue
		case 0:
			slog.Info("supervisor: child exited cleanly, stopping supervisor", "target", target)
			return nil
		default:
			slog.Warn("supervisor: child exited with non-zero code, restarting",
				"target", target, "exit_code", exitCode)
			continue
		}
	}
}

// spawnChild starts os.Args[0] with the given target argument and the
// CUSTOMCLAW_SUPERVISED=1 environment variable injected.
func spawnChild(target string) (*exec.Cmd, error) {
	cmd := exec.Command(os.Args[0], target) //nolint:gosec // intentional self-respawn
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		SupervisedEnv+"=1",
		RuntimeTargetEnv+"="+target,
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start child process: %w", err)
	}
	slog.Info("supervisor: child started", "target", target, "pid", cmd.Process.Pid)
	return cmd, nil
}

// waitChild monitors the child process and Redis restart requests concurrently.
// It returns (exitCode, nil) when the child exits for any reason, or
// (0, ctx.Err()) when the parent context is cancelled.
//
// cmd.Wait() is called exactly once, in the background goroutine. All callers
// that need to wait for process exit receive results via exitCh to avoid
// concurrent Wait() calls which would trigger a data race.
func waitChild(ctx context.Context, rdb *redis.Client, target string, cmd *exec.Cmd) (int, error) {
	// exitCh carries the child's exit code once Wait returns.
	// Buffered so the goroutine never blocks if no one reads.
	exitCh := make(chan int, 1)
	go func() {
		err := cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCh <- exitErr.ExitCode()
				return
			}
		}
		exitCh <- 0
	}()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("supervisor: context cancelled, terminating child",
				"target", target, "pid", cmd.Process.Pid)
			// Signal the child to stop, then wait for the single Wait() goroutine.
			signalChild(cmd)
			waitForExit(cmd, exitCh)
			return 0, ctx.Err()

		case code := <-exitCh:
			return code, nil

		case <-ticker.C:
			if rdb == nil {
				continue
			}
			req, err := ConsumeRestartRequest(ctx, rdb, target)
			if err != nil {
				slog.Warn("supervisor: error checking restart request", "error", err)
				continue
			}
			if req == nil {
				continue
			}
			slog.Info("supervisor: restart request received",
				"target", target,
				"requested_by", req.RequestedBy,
				"reason", req.Reason,
			)
			signalChild(cmd)
			// Drain the exit channel so the goroutine does not block.
			<-exitCh
			return RestartExitCode, nil
		}
	}
}

// signalChild sends SIGTERM (os.Interrupt) to the child process. If the signal
// fails (e.g. process already gone) it falls back to SIGKILL.
// It does NOT call cmd.Wait() — that is the responsibility of the single goroutine
// started in waitChild.
func signalChild(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	slog.Info("supervisor: sending SIGTERM", "pid", cmd.Process.Pid)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		slog.Warn("supervisor: SIGTERM failed, sending SIGKILL", "error", err)
		_ = cmd.Process.Kill()
	}
}

// waitForExit blocks until the child has exited, enforcing sigTermTimeout.
// It reads from exitCh (the channel populated by the single cmd.Wait() goroutine)
// rather than calling cmd.Wait() itself, avoiding concurrent-Wait data races.
func waitForExit(cmd *exec.Cmd, exitCh <-chan int) {
	select {
	case <-exitCh:
		slog.Info("supervisor: child exited after SIGTERM")
	case <-time.After(sigTermTimeout):
		slog.Warn("supervisor: child did not exit after SIGTERM timeout, sending SIGKILL",
			"timeout", sigTermTimeout)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-exitCh // wait for the goroutine's cmd.Wait() to finish
	}
}

// terminateChild sends SIGTERM to the child and waits up to sigTermTimeout.
// If the child does not exit in time, SIGKILL is sent.
// This is called from contexts where no exitCh goroutine is active
// (e.g. TestTerminateChildClean where the process has already exited).
func terminateChild(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	slog.Info("supervisor: sending SIGTERM", "pid", cmd.Process.Pid)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		slog.Warn("supervisor: SIGTERM failed, sending SIGKILL", "error", err)
		_ = cmd.Process.Kill()
		return
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("supervisor: child exited after SIGTERM")
	case <-time.After(sigTermTimeout):
		slog.Warn("supervisor: child did not exit after SIGTERM timeout, sending SIGKILL",
			"timeout", sigTermTimeout)
		_ = cmd.Process.Kill()
		<-done
	}
}
