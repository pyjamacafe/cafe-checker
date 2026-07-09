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
	Files    []File `json:"files"`
	Language string `json:"language"`
}

type SubmitResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
	TimedOut bool   `json:"timedOut"`
	Error    string `json:"error,omitempty"`
	Phase    string `json:"phase,omitempty"`
}

type LangConfig struct {
	Compile []string
	Link    []string
	Runner  []string
}

var languages = map[string]LangConfig{
	"c": {
		Compile: []string{"gcc", "-x", "c", "-std=c11", "-Wall", "-O0", "-o", "program", "main.c"},
		Runner:  []string{"./program"},
	},
	"cpp": {
		Compile: []string{"g++", "-x", "c++", "-std=c++17", "-Wall", "-O0", "-o", "program", "main.cpp"},
		Runner:  []string{"./program"},
	},
	"python": {
		Runner: []string{"python3", "main.py"},
	},
	"assembly": {
		Compile: []string{"as", "-o", "program.o", "main.s"},
		Link:    []string{"gcc", "-o", "program", "program.o"},
		Runner:  []string{"./program"},
	},
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

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

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

func submitHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Files) == 0 {
		json.NewEncoder(w).Encode(SubmitResponse{
			Stderr:   "No files provided",
			ExitCode: 1,
		})
		return
	}

	cfg, ok := languages[req.Language]
	if !ok {
		json.NewEncoder(w).Encode(SubmitResponse{
			Stderr:   fmt.Sprintf("Unsupported language: %s", req.Language),
			ExitCode: 1,
		})
		return
	}

	dir := randomDir()
	if err := writeFiles(dir, req.Files); err != nil {
		json.NewEncoder(w).Encode(SubmitResponse{
			Stderr:   err.Error(),
			ExitCode: 1,
		})
		return
	}
	defer os.RemoveAll(dir)

	if len(cfg.Compile) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		res := runCmd(ctx, cfg.Compile[0], cfg.Compile[1:], dir)
		cancel()
		if res.ExitCode != 0 {
			res.Phase = "compile"
			json.NewEncoder(w).Encode(res)
			return
		}
	}

	if len(cfg.Link) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		res := runCmd(ctx, cfg.Link[0], cfg.Link[1:], dir)
		cancel()
		if res.ExitCode != 0 {
			res.Phase = "link"
			json.NewEncoder(w).Encode(res)
			return
		}
	}

	if len(cfg.Runner) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		res := runCmd(ctx, cfg.Runner[0], cfg.Runner[1:], dir)
		cancel()
		json.NewEncoder(w).Encode(res)
		return
	}

	json.NewEncoder(w).Encode(SubmitResponse{})
}

func main() {
	os.MkdirAll(tmpDir, 0777)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hey there :)"))
	})
	mux.HandleFunc("/api/submit", submitHandler)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("Hey there :)"))
	})

	log.Printf("Judge server listening on port %s", port)
	log.Fatal(http.ListenAndServe(port, mux))
}
