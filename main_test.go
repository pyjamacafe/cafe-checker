package main

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A command that backgrounds a process must not leave it running after runCmd
// returns. The shell exits immediately; the backgrounded `sleep` is a child of
// the shell and would be orphaned if we only killed the direct child.
func TestRunCmdReapsBackgroundChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res := runCmd(ctx, "sh", []string{"-c", "sleep 300 & echo $!"}, t.TempDir())

	pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatalf("could not parse background pid from %q", res.Stdout)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		// Signal 0 only checks for existence; ESRCH means the process is gone.
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background child %d still alive after runCmd returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A command that runs past the deadline must be killed (and reported as timed
// out) rather than left running.
func TestRunCmdTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	res := runCmd(ctx, "sh", []string{"-c", "sleep 30"}, t.TempDir())
	if !res.TimedOut {
		t.Fatalf("expected TimedOut=true, got %+v", res)
	}
}
