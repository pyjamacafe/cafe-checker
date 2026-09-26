package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	tmpDir  = "/tmp/judge"
	port    = ":4000"
	timeout = 5 * time.Second
)

type File struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type SubmitRequest struct {
	Files   []File `json:"files"`
	Command string `json:"command"`
}

type SubmitResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
	TimedOut bool   `json:"timedOut"`
	Error    string `json:"error,omitempty"`
	Phase    string `json:"phase,omitempty"`
}

func randomDir() string {
	b := make([]byte, 8)
	rand.Read(b)
	return filepath.Join(tmpDir, hex.EncodeToString(b))
}

func writeFiles(dir string, files []File) error {
	if err := os.MkdirAll(dir, 0777); err != nil {
		return err
	}
	for _, f := range files {
		path := filepath.Join(dir, f.Name)
		if err := os.WriteFile(path, []byte(f.Content), 0644); err != nil {
			return err
		}
	}
	return nil
}

func runCmd(ctx context.Context, name string, args []string, dir string) SubmitResponse {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// Run in a dedicated process group so we can kill the whole tree (the shell
	// and anything it spawns), not just the direct child. Otherwise a command
	// that backgrounds a process would leave it orphaned and outlive the request.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// If a grandchild keeps stdout/stderr open after the main process exits,
	// don't block forever waiting for the pipes to close.
	cmd.WaitDelay = 2 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Reap anything still left in the group (e.g. backgrounded children). This is
	// a no-op when the group is already empty (ESRCH).
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var exitCode int
	timedOut := false
	var errMsg string

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			errMsg = err.Error()
			exitCode = 1
		}
	}

	return SubmitResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		TimedOut: timedOut,
		Error:    errMsg,
	}
}

func logRequest(r *http.Request, status string, dur time.Duration, extra ...string) {
	fields := fmt.Sprintf("method=%s path=%s status=%s duration=%s", r.Method, r.URL.Path, status, dur)
	for _, e := range extra {
		fields += " " + e
	}
	log.Println(fields)
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		logRequest(r, "204", time.Since(start))
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		logRequest(r, "405", time.Since(start))
		return
	}

	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		logRequest(r, "400", time.Since(start))
		return
	}
	log.Printf("submit command=%q files=%d", req.Command, len(req.Files))

	if len(req.Files) == 0 {
		json.NewEncoder(w).Encode(SubmitResponse{
			Stderr:   "No files provided",
			ExitCode: 1,
		})
		logRequest(r, "200", time.Since(start), "phase=reject reason=no_files")
		return
	}

	// The judge runs only the command a lesson defines via {{< run_check >}}.
	// There is no built-in per-language compile/run.
	if strings.TrimSpace(req.Command) == "" {
		json.NewEncoder(w).Encode(SubmitResponse{
			Stderr:   "No check command provided",
			ExitCode: 1,
		})
		logRequest(r, "200", time.Since(start), "phase=reject reason=no_command")
		return
	}

	dir := randomDir()
	// Always clean up the submitted files once the command has finished — even if
	// writing them failed partway or the command errored/timed out.
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("cleanup failed dir=%s err=%v", dir, err)
		}
	}()
	if err := writeFiles(dir, req.Files); err != nil {
		json.NewEncoder(w).Encode(SubmitResponse{
			Stderr:   err.Error(),
			ExitCode: 1,
		})
		logRequest(r, "200", time.Since(start), "phase=reject reason=write_error")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	res := runCmd(ctx, "sh", []string{"-c", req.Command}, dir)
	cancel()
	res.Phase = "check"
	log.Printf("check command=%q exit=%d timedOut=%v", req.Command, res.ExitCode, res.TimedOut)
	json.NewEncoder(w).Encode(res)
	logRequest(r, "200", time.Since(start), fmt.Sprintf("phase=check exit=%d timedOut=%v", res.ExitCode, res.TimedOut))
}

func main() {
	os.MkdirAll(tmpDir, 0777)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			logRequest(r, "404", time.Since(start))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hey there :)"))
		logRequest(r, "200", time.Since(start))
	})
	mux.HandleFunc("/api/submit", submitHandler)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("Hey there :)"))
		logRequest(r, "200", time.Since(start))
	})

	log.Printf("Judge server listening on port %s", port)
	log.Fatal(http.ListenAndServe(port, mux))
}
