// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
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

func TestReadOnlyCaptureFailurePreservesVerifiedSource(t *testing.T) {
	w := testWorker(t)
	t.Setenv("SOURCE_STOPPED", "true")
	before, e := inventory(w.data)
	if e != nil {
		t.Fatal(e)
	}
	// A retained partial capture prevents staging creation without touching state.
	if e := os.Mkdir(filepath.Join(w.staging, "capture-in-progress"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := w.capture(); e == nil {
		t.Fatal("capture unexpectedly overwrote partial evidence")
	}
	if !w.result.SourceIntact {
		t.Fatal("verified read-only source was reported as uncertain")
	}
	after, e := inventory(w.data)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("capture failure changed source: %v", e)
	}
	if _, e := w.cleanState(); e != nil {
		t.Fatalf("capture failure invalidated native restart: %v", e)
	}
}

func TestCaptureStagingLockFailurePreservesSourceAndRetries(t *testing.T) {
	w := testWorker(t)
	t.Setenv("SOURCE_STOPPED", "true")
	lockPath := filepath.Join(w.staging, "lock")
	if e := os.Mkdir(lockPath, 0700); e != nil {
		t.Fatal(e)
	}
	if e := w.run("capture"); e == nil || !w.result.SourceIntact {
		t.Fatalf("staging-only failure invalidated source: %v %+v", e, w.result)
	}
	if s, e := w.cleanState(); e != nil || s.Operation != "prepared" {
		t.Fatalf("staging lock failure changed source state: %+v %v", s, e)
	}
	if e := os.Remove(lockPath); e != nil {
		t.Fatal(e)
	}
	if e := w.run("capture"); e != nil {
		t.Fatalf("capture could not resume after staging fault removal: %v", e)
	}
	if _, e := verifyPoint(filepath.Join(w.staging, "capture"), w.id); e != nil {
		t.Fatalf("retried capture is not usable: %v", e)
	}
}

func TestCaptureCannotClaimIntactUnverifiedSource(t *testing.T) {
	for _, cause := range []string{"mutation", "shutdown", "version", "missing-database"} {
		t.Run(cause, func(t *testing.T) {
			w := testWorker(t)
			t.Setenv("SOURCE_STOPPED", "true")
			var e error
			switch cause {
			case "mutation":
				e = w.beginMutation("prepare")
			case "shutdown":
				e = atomicJSON(w.shutdownPath(), w.id)
			case "version":
				w.version = "16.0.7"
			case "missing-database":
				e = os.Remove(filepath.Join(w.data, "data/forgejo.db"))
			}
			if e != nil {
				t.Fatal(e)
			}
			if e := w.capture(); e == nil || w.result.SourceIntact {
				t.Fatalf("unverified source allowed: error=%v intact=%v", e, w.result.SourceIntact)
			}
		})
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

func TestNativeTrustBundleValidation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	for _, tc := range []struct {
		name      string
		data      []byte
		wantError bool
	}{
		{name: "valid certificate", data: certificate},
		{name: "multiple certificates", data: append(append([]byte{}, certificate...), certificate...)},
		{name: "commented public CA bundle", data: append(append([]byte("# Public roots\n"), certificate...), []byte("# End of bundle\n")...)},
		{name: "empty", wantError: true},
		{name: "invalid PEM", data: []byte("not a certificate"), wantError: true},
		{name: "invalid DER", data: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}), wantError: true},
		{name: "private key", data: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid")}), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ca-bundle.pem")
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SSL_CERT_FILE", path)
			if err := validateNativeTrust(); (err != nil) != tc.wantError {
				t.Fatalf("validation = %v, want error %v", err, tc.wantError)
			}
		})
	}
	for _, path := range []string{"", "relative.pem", filepath.Join(t.TempDir(), "missing.pem")} {
		t.Run("invalid path "+path, func(t *testing.T) {
			t.Setenv("SSL_CERT_FILE", path)
			if err := validateNativeTrust(); err == nil {
				t.Fatal("supplied invalid trust accepted")
			}
			w := &worker{id: identity{"installation", "instance"}, data: filepath.Join(t.TempDir(), "uncreated"), staging: t.TempDir(), version: "17.0.1", operation: "prepare"}
			if err := w.run("prepare"); err == nil || !strings.Contains(err.Error(), "SSL_CERT_FILE") {
				t.Fatalf("worker did not refuse invalid trust: %v", err)
			}
			if _, err := os.Stat(w.data); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid trust mutated state: %v", err)
			}
		})
	}
}

func TestNativeTrustEnvironmentIsNarrow(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", "/configuration/ca-bundle.pem")
	t.Setenv("SSL_CERT_DIR", "/untrusted")
	t.Setenv("RESTIC_PASSWORD", "must-not-leak")
	t.Setenv("GIT_SSL_NO_VERIFY", "true")
	w := &worker{data: "/data"}
	for _, args := range [][]string{{"web"}, {"admin", "auth", "add-oauth"}, {"admin", "auth", "update-oauth"}} {
		cmd := w.nativeCommand("17.0.1", "/data/app.ini", args...)
		found := false
		for _, entry := range cmd.Env {
			if entry == "SSL_CERT_FILE=/configuration/ca-bundle.pem" {
				found = true
			}
			for _, forbidden := range []string{"SSL_CERT_DIR=", "RESTIC_PASSWORD=", "GIT_SSL_NO_VERIFY="} {
				if strings.HasPrefix(entry, forbidden) {
					t.Fatalf("native environment leaked %s", forbidden)
				}
			}
		}
		if !found {
			t.Fatalf("%v lost native trust", args)
		}
	}
	t.Setenv("SSL_CERT_FILE", "")
	if err := os.Unsetenv("SSL_CERT_FILE"); err != nil {
		t.Fatal(err)
	}
	if err := validateNativeTrust(); err != nil {
		t.Fatalf("omitted optional trust changed defaults: %v", err)
	}
	for _, entry := range w.nativeEnv() {
		if strings.HasPrefix(entry, "SSL_CERT_FILE=") {
			t.Fatal("ordinary native environment overrides default trust")
		}
	}
}

func TestNativeTrustHTTPSHelper(t *testing.T) {
	target := os.Getenv("BARTL_TRUST_TEST_URL")
	if target == "" {
		return
	}
	response, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}

func TestNativeTrustVerifiesTLS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SSL_CERT_FILE is the combined image's native Linux Go trust interface")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "ca-bundle.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		bundle    string
		target    string
		wantError string
	}{
		{name: "untrusted issuer", bundle: "/dev/null", target: server.URL, wantError: "unknown authority"},
		{name: "supplied trust", bundle: path, target: server.URL},
		{name: "wrong hostname", bundle: path, target: strings.Replace(server.URL, "127.0.0.1", "localhost", 1), wantError: "certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SSL_CERT_FILE", tc.bundle)
			cmd := exec.Command(executable, "-test.run=^TestNativeTrustHTTPSHelper$")
			cmd.Env = append((&worker{data: t.TempDir()}).nativeEnv(), "BARTL_TRUST_TEST_URL="+tc.target, "SSL_CERT_DIR="+t.TempDir())
			output, err := cmd.CombinedOutput()
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("trusted native TLS failed: %v: %s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), tc.wantError) {
				t.Fatalf("wanted TLS refusal containing %q, got %v: %s", tc.wantError, err, output)
			}
		})
	}
}

func TestOwnedPartialCaptureRetriesAfterWriteFault(t *testing.T) {
	if os.Getenv("BARTL_CAPTURE_WRITE_FAULT") != "true" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOwnedPartialCaptureRetriesAfterWriteFault$")
		cmd.Env = append(os.Environ(), "BARTL_CAPTURE_WRITE_FAULT=true")
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("capture write-fault subprocess: %v\n%s", e, out)
		}
		return
	}
	w := testWorker(t)
	t.Setenv("SOURCE_STOPPED", "true")
	if e := os.WriteFile(filepath.Join(w.data, "data/forgejo.db"), bytes.Repeat([]byte("d"), 8192), 0600); e != nil {
		t.Fatal(e)
	}
	before, e := inventory(w.data)
	if e != nil {
		t.Fatal(e)
	}
	var limit syscall.Rlimit
	if e := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit); e != nil {
		t.Fatal(e)
	}
	signal.Ignore(syscall.SIGXFSZ)
	if e := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 2048, Max: limit.Max}); e != nil {
		t.Fatal(e)
	}
	e = w.run("capture")
	if restoreErr := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if e == nil || !w.result.SourceIntact {
		t.Fatalf("write fault did not retain intact-source distinction: %v %+v", e, w.result)
	}
	var partial manifest
	if e := readJSON(filepath.Join(w.staging, "capture-in-progress/manifest.json"), &partial); e != nil || partial.Operation != w.operation || partial.identity != w.id {
		t.Fatalf("failed copy lacks owned manifest: %v %+v", e, partial)
	}
	if _, e := w.cleanState(); e != nil {
		t.Fatalf("explicit native restart lost its usable source: %v", e)
	}
	after, e := inventory(w.data)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("capture failure changed native state: %v", e)
	}
	w.operation = "subsequent-capture"
	w.result = result{}
	if e := w.run("capture"); e != nil {
		t.Fatalf("ordinary retry on same staging failed after fault removal: %v", e)
	}
	point, e := verifyPoint(filepath.Join(w.staging, "capture"), w.id)
	if e != nil || point.Operation != w.operation || w.result.Capture != w.operation {
		t.Fatalf("same-staging retry did not produce new complete capture: %v %+v", e, point)
	}
}

func TestCaptureProtectsForeignPartialAndCompleteCapture(t *testing.T) {
	for _, kind := range []string{"foreign-partial", "protected-complete"} {
		t.Run(kind, func(t *testing.T) {
			w := testWorker(t)
			t.Setenv("SOURCE_STOPPED", "true")
			root := filepath.Join(w.staging, "capture-in-progress")
			if kind == "protected-complete" {
				root = filepath.Join(w.staging, "capture")
			}
			foreign := manifest{state: state{identity{"foreign", "instance"}, w.version, "old-operation"}, Format: 1}
			if e := atomicJSON(filepath.Join(root, "manifest.json"), foreign); e != nil {
				t.Fatal(e)
			}
			before, e := inventory(root)
			if e != nil {
				t.Fatal(e)
			}
			if e := w.run("capture"); e == nil {
				t.Fatal("capture removed protected or foreign staging")
			}
			after, e := inventory(root)
			if e != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("protected staging changed: %v", e)
			}
		})
	}
}
