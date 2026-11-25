// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type identity struct {
	Installation string `json:"installation"`
	Instance     string `json:"instance"`
}
type state struct {
	identity
	Version   string `json:"version"`
	Operation string `json:"operation"`
}
type result struct {
	identity
	Operation string `json:"operation"`
	Command   string `json:"command"`
	Success   bool   `json:"success"`
	Version   string `json:"version"`
	Message   string `json:"message"`
	Snapshot  string `json:"snapshot,omitempty"`
	Capture   string `json:"capture,omitempty"`
}
type worker struct {
	id                                        identity
	data, staging, config, version, operation string
	result                                    result
}

func main() {
	w := &worker{id: identity{os.Getenv("INSTALLATION_ID"), os.Getenv("INSTANCE_ID")}, data: env("DATA_DIR", "/data"), staging: env("STAGING_DIR", "/staging"), config: env("CONFIG_FILE", "/configuration/app.ini"), version: os.Getenv("VERSION"), operation: os.Getenv("OPERATION_ID")}
	command := ""
	if len(os.Args) == 2 {
		command = os.Args[1]
	}
	w.result = result{identity: w.id, Operation: w.operation, Command: command, Version: w.version}
	err := w.run(command)
	w.result.Success = err == nil
	if err != nil {
		w.result.Message = err.Error()
	} else {
		w.result.Message = command + " completed"
	}
	bytes, _ := json.Marshal(w.result)
	fmt.Println(string(bytes))
	if e := os.WriteFile(env("TERMINATION_LOG", "/dev/termination-log"), bytes, 0600); e != nil {
		fmt.Fprintln(os.Stderr, "write termination result:", e)
		if err == nil {
			err = e
		}
	}
	if err != nil {
		os.Exit(1)
	}
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func (w *worker) run(command string) error {
	if w.id.Installation == "" || w.id.Instance == "" || (command != "serve" && w.operation == "") {
		return errors.New("installation and instance identity, and finite-operation identity, are required")
	}
	if !supported(w.version) {
		return errors.New("unsupported requested version")
	}
	if !filepath.IsAbs(w.data) || !filepath.IsAbs(w.staging) {
		return errors.New("state and staging paths must be absolute")
	}
	switch command {
	case "prepare", "serve", "capture", "publish":
	default:
		return errors.New("expected prepare, serve, capture or publish")
	}
	// Go's system-root loader otherwise ignores missing/malformed optional
	// files. Refuse supplied invalid trust before mutating any application state.
	if command == "prepare" || command == "serve" {
		if err := validateNativeTrust(); err != nil {
			return err
		}
	}
	// Kernel exclusion complements the controller's single-writer lifecycle. It
	// is released on process death, unlike the durable interrupted-mutation mark.
	lockRoot := filepath.Join(w.data, ".bartl")
	if command == "publish" {
		lockRoot = w.staging
	}
	if err := os.MkdirAll(lockRoot, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(lockRoot, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("conflicting worker holds state lock")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	switch command {
	case "prepare":
		return w.prepare()
	case "serve":
		return w.serve()
	case "capture":
		return w.capture()
	default:
		return w.publish()
	}
}
func supported(v string) bool { return v == "16.0.7" || v == "17.0.1" }
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
func readJSON(path string, value any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, value); e != nil {
		return fmt.Errorf("invalid metadata %s: %w", path, e)
	}
	return nil
}
func atomicJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	return atomicFile(path, append(b, '\n'), 0600)
}
func atomicFile(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".write-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (w *worker) statePath() string    { return filepath.Join(w.data, ".bartl/state.json") }
func (w *worker) mutationPath() string { return filepath.Join(w.data, ".bartl/mutation.json") }
func (w *worker) shutdownPath() string { return filepath.Join(w.data, ".bartl/native-running.json") }
func (w *worker) confirmShutdown() error {
	if e := os.Remove(w.shutdownPath()); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return syncDir(filepath.Dir(w.shutdownPath()))
}
func (w *worker) cleanState() (state, error) {
	var s state
	pending, e := exists(w.mutationPath())
	if e != nil {
		return s, e
	}
	if pending {
		return s, errors.New("interrupted state mutation: preserve this failed target and retry on independent empty state")
	}
	if e = readJSON(w.statePath(), &s); e != nil {
		return s, e
	}
	if s.identity != w.id || !supported(s.Version) || s.Operation == "" {
		return s, errors.New("state identity or version metadata does not match")
	}
	present, e := exists(filepath.Join(w.data, "data/forgejo.db"))
	if e != nil {
		return s, e
	}
	if !present {
		return s, errors.New("committed native SQLite state is missing")
	}
	return s, nil
}
func (w *worker) beginMutation(kind string) error {
	return atomicJSON(w.mutationPath(), struct {
		state
		Kind    string    `json:"kind"`
		Started time.Time `json:"started"`
	}{state{w.id, w.version, w.operation}, kind, time.Now().UTC()})
}
func (w *worker) commitState() error {
	if e := atomicJSON(w.statePath(), state{w.id, w.version, w.operation}); e != nil {
		return e
	}
	if e := os.Remove(w.mutationPath()); e != nil {
		return e
	}
	return syncDir(filepath.Dir(w.mutationPath()))
}
