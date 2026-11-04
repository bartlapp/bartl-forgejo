// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testWorker(t *testing.T) *worker {
	t.Helper()
	w := &worker{id: identity{"installation", "instance"}, data: t.TempDir(), staging: t.TempDir(), version: "17.0.1", operation: "operation"}
	if e := atomicJSON(w.statePath(), state{w.id, w.version, "prepared"}); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(w.data, "data"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(w.data, "data/forgejo.db"), []byte("database"), 0600); e != nil {
		t.Fatal(e)
	}
	return w
}

func TestSnapshotLookupRequiresCleanErrorChannel(t *testing.T) {
	point := `[{"id":"` + strings.Repeat("a", 64) + `","time":"2026-11-04T00:00:00Z"}]`
	for _, tc := range []struct {
		name        string
		output      string
		diagnostics string
		exit        string
		wantCount   int
		wantError   bool
	}{
		{name: "successful empty lookup", output: "[]", exit: "0"},
		{name: "successful matching point", output: point, exit: "0", wantCount: 1},
		{name: "failed list exits zero with empty output", output: "[]", diagnostics: "List(snapshot) failed: Decode: invalid character\ncould not load snapshots: Decode: invalid character\n", exit: "0", wantError: true},
		{name: "failed object omitted from partial list", output: point, diagnostics: "Ignoring \"snapshot\": invalid data returned\n", exit: "0", wantError: true},
		{name: "native command failure", output: "[]", exit: "1", wantError: true},
		{name: "malformed native output", output: "{", exit: "0", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", `printf '%s' "$1"; printf '%s' "$2" >&2; exit "$3"`, "snapshot-lookup", tc.output, tc.diagnostics, tc.exit)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			points, e := lookupSnapshots(cmd)
			if (e != nil) != tc.wantError {
				t.Fatalf("lookup error = %v, want error %v", e, tc.wantError)
			}
			if len(points) != tc.wantCount {
				t.Fatalf("lookup returned %d points, want %d", len(points), tc.wantCount)
			}
			if diagnostics.String() != tc.diagnostics {
				t.Fatalf("native diagnostics were not forwarded: %q", diagnostics.String())
			}
		})
	}
}

func TestOperationIdentityOnlyRequiredForFiniteJobs(t *testing.T) {
	w := testWorker(t)
	w.operation = ""
	w.config = filepath.Join(t.TempDir(), "missing.ini")
	for _, command := range []string{"prepare", "capture", "publish"} {
		if e := w.run(command); e == nil || !strings.Contains(e.Error(), "finite-operation identity") {
			t.Fatalf("%s accepted absent operation: %v", command, e)
		}
	}
	if e := w.run("serve"); e == nil || strings.Contains(e.Error(), "identity") {
		t.Fatalf("serve must reach configuration without operation ID: %v", e)
	}
}

func TestCopyTreePreservesModesDespiteUmask(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	directory := filepath.Join(source, "shared")
	if e := os.Mkdir(directory, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(directory, 0770|os.ModeSetgid); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(directory, "group-writable")
	if e := os.WriteFile(file, []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(file, 0660); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(target, "shared"), 0700); e != nil {
		t.Fatal(e)
	}
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	if e := copyTree(source, target); e != nil {
		t.Fatal(e)
	}
	before, e := inventory(source)
	if e != nil {
		t.Fatal(e)
	}
	after, e := inventory(target)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("copied modes differ: source=%+v target=%+v", before, after)
	}
}

func TestUncertainShutdownForbidsCaptureButAllowsRestart(t *testing.T) {
	w := testWorker(t)
	t.Setenv("SOURCE_STOPPED", "true")
	if e := atomicJSON(w.shutdownPath(), w.id); e != nil {
		t.Fatal(e)
	}
	if _, e := w.cleanState(); e != nil {
		t.Fatalf("ordinary native restart must remain possible: %v", e)
	}
	if e := w.capture(); e == nil || !strings.Contains(e.Error(), "shutdown is unconfirmed") {
		t.Fatalf("uncertain native shutdown allowed capture: %v", e)
	}
}

func TestNativeLifecycleHelper(t *testing.T) {
	mode := os.Getenv("BARTL_NATIVE_TEST")
	if mode == "" {
		return
	}
	if mode == "supervisor" {
		w := &worker{id: identity{"installation", "instance"}, data: os.Getenv("BARTL_NATIVE_DATA")}
		executable, e := os.Executable()
		if e != nil {
			os.Exit(2)
		}
		cmd := exec.Command(executable, "-test.run=^TestNativeLifecycleHelper$")
		cmd.Env = append(os.Environ(), "BARTL_NATIVE_TEST=graceful")
		if e = w.serveNative(cmd, make(chan os.Signal), time.Second); e != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	listener, e := net.Listen("tcp", "127.0.0.1:3000")
	if e != nil {
		os.Exit(2)
	}
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	if e = os.WriteFile(os.Getenv("BARTL_NATIVE_READY"), []byte(strconv.Itoa(os.Getpid())), 0600); e != nil {
		os.Exit(3)
	}
	if mode == "crash" {
		os.Exit(4)
	}
	if mode == "exit-zero" {
		os.Exit(0)
	}
	for {
		<-signals
		if mode == "graceful" {
			os.Exit(0)
		}
	}
}

func TestNativeShutdownRecovery(t *testing.T) {
	w := testWorker(t)
	t.Setenv("SOURCE_STOPPED", "true")
	for _, mode := range []string{"crash", "exit-zero", "forced", "graceful"} {
		t.Run(mode, func(t *testing.T) {
			executable, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			readyPath := filepath.Join(t.TempDir(), "ready")
			cmd := exec.Command(executable, "-test.run=^TestNativeLifecycleHelper$")
			cmd.Env = append(os.Environ(), "BARTL_NATIVE_TEST="+mode, "BARTL_NATIVE_READY="+readyPath)
			signals := make(chan os.Signal, 1)
			done := make(chan error, 1)
			go func() { done <- w.serveNative(cmd, signals, 50*time.Millisecond) }()
			deadline := time.After(5 * time.Second)
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
		waitReady:
			for {
				select {
				case <-deadline:
					if cmd.Process != nil {
						cmd.Process.Kill()
					}
					t.Fatal("native helper did not start")
				case <-ticker.C:
					if present, _ := exists(readyPath); present {
						break waitReady
					}
				}
			}
			if mode == "forced" || mode == "graceful" {
				// Allow the production health poll to confirm native startup.
				time.Sleep(750 * time.Millisecond)
				signals <- syscall.SIGTERM
			}
			e = <-done
			pending, statErr := exists(w.shutdownPath())
			if statErr != nil {
				t.Fatal(statErr)
			}
			if mode == "graceful" {
				if e != nil || pending {
					t.Fatalf("successful restart and graceful stop did not clear uncertainty: %v marker=%v", e, pending)
				}
			} else if e == nil || !pending {
				t.Fatalf("failed shutdown lost uncertainty: %v marker=%v", e, pending)
			}
		})
	}
	if e := w.capture(); e != nil {
		t.Fatalf("successful native recovery and graceful stop did not permit capture: %v", e)
	}
}

func TestPublicationReceiptSurvivesInterruptedCleanup(t *testing.T) {
	w := testWorker(t)
	receipt := publicationResult{state{w.id, w.version, w.operation}, strings.Repeat("a", 64), strings.Repeat("b", 64)}
	if e := atomicJSON(filepath.Join(w.staging, "publication-result.json"), receipt); e != nil {
		t.Fatal(e)
	}
	// A process died after the durable receipt and capture rename, during deletion.
	retired := filepath.Join(w.staging, "published-capture")
	if e := os.Mkdir(retired, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(retired, "partial"), []byte("remaining"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := w.cleanupPublication(receipt); e != nil {
		t.Fatal(e)
	}
	var persisted publicationResult
	if e := readJSON(filepath.Join(w.staging, "publication-result.json"), &persisted); e != nil || persisted != receipt {
		t.Fatalf("publication result lost: %+v %v", persisted, e)
	}
	if w.result.Snapshot != receipt.Snapshot || w.result.Capture != w.operation {
		t.Fatalf("retry lost original result: %+v", w.result)
	}
	if present, e := exists(retired); e != nil || present {
		t.Fatalf("interrupted cleanup not completed: %v %v", present, e)
	}
	// A receipt for an older operation must never delete the next capture.
	if e := atomicJSON(filepath.Join(w.staging, "capture/manifest.json"), manifest{state: state{w.id, w.version, "next"}}); e != nil {
		t.Fatal(e)
	}
	if e := w.cleanupPublication(receipt); e == nil {
		t.Fatal("old receipt deleted another operation's protected capture")
	}
	if _, e := os.Stat(filepath.Join(w.staging, "capture/manifest.json")); errors.Is(e, os.ErrNotExist) {
		t.Fatal("new capture was removed")
	}
}

func TestPublicationRetryRequiresVerifiedSnapshot(t *testing.T) {
	w := testWorker(t)
	t.Setenv("RESTIC_REPOSITORY", filepath.Join(t.TempDir(), "unavailable-repository"))
	receipt := publicationResult{state{w.id, w.version, w.operation}, strings.Repeat("a", 64), strings.Repeat("b", 64)}
	if e := atomicJSON(filepath.Join(w.staging, "publication-result.json"), receipt); e != nil {
		t.Fatal(e)
	}
	if e := atomicJSON(filepath.Join(w.staging, "capture/manifest.json"), manifest{state: receipt.state}); e != nil {
		t.Fatal(e)
	}
	if e := w.publish(); e == nil {
		t.Fatal("retry claimed success without verifying recorded remote snapshot")
	}
	if present, e := exists(filepath.Join(w.staging, "capture")); e != nil || !present {
		t.Fatalf("unverified retry deleted protected capture: %v %v", present, e)
	}
}

func TestWorkerDeathRetainsShutdownUncertainty(t *testing.T) {
	w := testWorker(t)
	t.Setenv("SOURCE_STOPPED", "true")
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(executable, "-test.run=^TestNativeLifecycleHelper$")
	cmd.Env = append(os.Environ(), "BARTL_NATIVE_TEST=supervisor", "BARTL_NATIVE_DATA="+w.data, "BARTL_NATIVE_READY="+readyPath)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()
	deadline := time.Now().Add(5 * time.Second)
	var nativePID int
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(readyPath)
		if err == nil {
			nativePID, e = strconv.Atoi(string(b))
			if e != nil {
				t.Fatal(e)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if nativePID == 0 {
		t.Fatal("supervised native process did not start")
	}
	native, e := os.FindProcess(nativePID)
	if e != nil {
		t.Fatal(e)
	}
	defer native.Kill()
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	cmd.Wait()
	if pending, e := exists(w.shutdownPath()); e != nil || !pending {
		t.Fatalf("worker death lost durable uncertainty: %v marker=%v", e, pending)
	}
	if e = w.capture(); e == nil {
		t.Fatal("worker death authorized capture")
	}
	if _, e = w.cleanState(); e != nil {
		t.Fatalf("worker death prohibited native recovery restart: %v", e)
	}
}

func TestNativeStartFailureRetainsUncertainty(t *testing.T) {
	w := testWorker(t)
	cmd := exec.Command(filepath.Join(t.TempDir(), "missing-native"))
	if e := w.serveNative(cmd, make(chan os.Signal), time.Millisecond); e == nil {
		t.Fatal("missing native executable unexpectedly started")
	}
	if pending, e := exists(w.shutdownPath()); e != nil || !pending {
		t.Fatalf("startup boundary lost durable uncertainty: %v marker=%v", e, pending)
	}
}
