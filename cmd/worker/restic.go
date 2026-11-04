// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

type snapshot struct {
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
}

var snapshotID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (w *worker) identityTag() string {
	b, _ := json.Marshal(w.id)
	sum := sha256.Sum256(b)
	return "identity-" + hex.EncodeToString(sum[:])
}
func (w *worker) resticCommand(directory string, args ...string) *exec.Cmd {
	options := []string{"--retry-lock", "2m"}
	if _, e := os.Stat("/backup/ca.crt"); e == nil {
		options = append(options, "--cacert", "/backup/ca.crt")
	}
	options = append(options, args...)
	cmd := exec.Command("restic", options...)
	cmd.Dir = directory
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "TMPDIR=/tmp", "RESTIC_CACHE_DIR=/tmp/restic-cache"}
	for _, key := range []string{"RESTIC_REPOSITORY", "RESTIC_PASSWORD_FILE", "RESTIC_REST_USERNAME", "RESTIC_REST_PASSWORD"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Stderr = os.Stderr
	return cmd
}
func (w *worker) restic(directory string, args ...string) ([]byte, error) {
	out, e := w.resticCommand(directory, args...).Output()
	if e != nil {
		return nil, fmt.Errorf("restic %s failed: %w", args[0], e)
	}
	return out, nil
}

type snapshotDiagnostics struct {
	io.Writer
	written bool
}

func (d *snapshotDiagnostics) Write(p []byte) (int, error) {
	d.written = d.written || len(p) != 0
	return d.Writer.Write(p)
}

func lookupSnapshots(cmd *exec.Cmd) ([]snapshot, error) {
	// Restic v0.19.1 FindFilteredSnapshots reports list/load errors through
	// printer.E (stderr), but runSnapshots can still return nil and print [] or
	// a partial list. JSON mode disables ordinary progress messages. Require a
	// clean error channel as well as exit success before interpreting absence.
	// Keep forwarding diagnostics without retaining an unbounded error log.
	diagnostics := snapshotDiagnostics{Writer: cmd.Stderr}
	if diagnostics.Writer == nil {
		diagnostics.Writer = os.Stderr
	}
	cmd.Stderr = &diagnostics
	out, e := cmd.Output()
	if e != nil {
		return nil, fmt.Errorf("restic snapshots failed: %w", e)
	}
	if diagnostics.written {
		return nil, errors.New("restic snapshots reported diagnostics; snapshot lookup is incomplete")
	}
	var points []snapshot
	if e = json.Unmarshal(out, &points); e != nil {
		return nil, e
	}
	return points, nil
}
func (w *worker) backup(directory, path, tag string) (string, error) {
	// Stream progress instead of retaining an output record for every file.
	cmd := w.resticCommand(directory, "backup", "--json", "--host", "bartl-forgejo", "--tag", tag+","+w.identityTag(), path)
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return "", e
	}
	if e = cmd.Start(); e != nil {
		return "", e
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	id := ""
	for scanner.Scan() {
		var event struct {
			Type string `json:"message_type"`
			ID   string `json:"snapshot_id"`
		}
		if e = json.Unmarshal(scanner.Bytes(), &event); e != nil {
			cmd.Process.Kill()
			cmd.Wait()
			return "", e
		}
		if event.Type == "summary" {
			id = event.ID
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return "", scanErr
	}
	if waitErr != nil {
		return "", fmt.Errorf("restic backup: %w", waitErr)
	}
	if !snapshotID.MatchString(id) {
		return "", errors.New("restic backup returned no complete snapshot ID")
	}
	return id, nil
}
func (w *worker) restoreSnapshot(id, target string) error {
	if !snapshotID.MatchString(id) {
		return errors.New("invalid Restic snapshot identity")
	}
	// Restic authenticates and verifies downloaded content; --verify additionally
	// rereads the reconstructed files. Native shared locks remain enabled.
	_, e := w.restic("/", "restore", id, "--target", target, "--verify")
	return e
}
func digestFile(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func (w *worker) restore() (bool, error) {
	if os.Getenv("RESTIC_REPOSITORY") == "" {
		return false, nil
	}
	points, e := lookupSnapshots(w.resticCommand(w.data, "snapshots", "--json", "--tag", "bartl-forgejo-complete,"+w.identityTag()))
	if e != nil {
		return false, e
	}
	if len(points) == 0 {
		return false, nil
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Time.Equal(points[j].Time) {
			return points[i].ID > points[j].ID
		}
		return points[i].Time.After(points[j].Time)
	})
	selected := points[0]
	root := filepath.Join(w.data, ".bartl/restore")
	if e = os.Mkdir(root, 0700); e != nil {
		return false, e
	}
	// No fallback: any failure of the selected complete point fails this target.
	if e = w.restoreSnapshot(selected.ID, root); e != nil {
		return false, e
	}
	point := filepath.Join(root, "capture")
	m, e := verifyPoint(point, w.id)
	if e != nil {
		return false, e
	}
	if m.Version != w.version && !(m.Version == "16.0.7" && w.version == "17.0.1") {
		return false, errors.New("selected recovery point requires unsupported transition or downgrade")
	}
	if e = copyTree(filepath.Join(point, "state"), w.data); e != nil {
		return false, e
	}
	if e = os.RemoveAll(root); e != nil {
		return false, e
	}
	w.result.Snapshot = selected.ID
	return true, nil
}

type publicationResult struct {
	state
	Snapshot string `json:"snapshot"`
	Manifest string `json:"manifest"`
}

func (w *worker) finishPublication(receipt publicationResult) error {
	if receipt.identity != w.id || receipt.Operation != w.operation || receipt.Version != w.version || !snapshotID.MatchString(receipt.Snapshot) || !snapshotID.MatchString(receipt.Manifest) {
		return errors.New("publication result does not match requested operation")
	}
	root, e := os.MkdirTemp(w.staging, "verify-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	if e = w.restoreSnapshot(receipt.Snapshot, root); e != nil {
		return e
	}
	point := filepath.Join(root, "capture")
	m, e := verifyPoint(point, w.id)
	if e != nil {
		return e
	}
	digest, e := digestFile(filepath.Join(point, "manifest.json"))
	if e != nil {
		return e
	}
	if m.Operation != receipt.Operation || m.Version != receipt.Version || digest != receipt.Manifest {
		return errors.New("recorded complete snapshot differs from publication result")
	}
	return w.cleanupPublication(receipt)
}

func (w *worker) cleanupPublication(receipt publicationResult) error {
	// The durable receipt remains after cleanup or a lost termination result.
	// Never remove a capture for a different operation on receipt replay.
	local := filepath.Join(w.staging, "capture")
	retired := filepath.Join(w.staging, "published-capture")
	if present, e := exists(local); e != nil {
		return e
	} else if present {
		var current manifest
		if e = readJSON(filepath.Join(local, "manifest.json"), &current); e != nil {
			return e
		}
		if current.identity != receipt.identity || current.Version != receipt.Version || current.Operation != receipt.Operation {
			return errors.New("protected capture belongs to another operation")
		}
		if e = os.Rename(local, retired); e != nil {
			return e
		}
		if e = syncDir(w.staging); e != nil {
			return e
		}
	}
	if e := os.RemoveAll(retired); e != nil {
		return e
	}
	if e := syncDir(w.staging); e != nil {
		return e
	}
	w.result.Snapshot = receipt.Snapshot
	w.result.Capture = receipt.Operation
	return nil
}
func (w *worker) publish() error {
	if os.Getenv("RESTIC_REPOSITORY") == "" {
		return errors.New("backup repository is required for publication")
	}
	receiptPath := filepath.Join(w.staging, "publication-result.json")
	var receipt publicationResult
	if e := readJSON(receiptPath, &receipt); e == nil {
		if receipt.Operation == w.operation {
			return w.finishPublication(receipt)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	point := filepath.Join(w.staging, "capture")
	m, e := verifyPoint(point, w.id)
	if e != nil {
		return e
	}
	if m.Operation != w.operation || m.Version != w.version {
		return errors.New("protected capture belongs to another operation or version")
	}
	w.result.Capture = m.Operation
	dataID, e := w.backup(w.staging, "capture", "bartl-forgejo-data")
	if e != nil {
		return e
	}
	verification, e := os.MkdirTemp(w.staging, "verify-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(verification)
	if e = w.restoreSnapshot(dataID, verification); e != nil {
		return e
	}
	recovered, e := verifyPoint(filepath.Join(verification, "capture"), w.id)
	if e != nil {
		return e
	}
	originalDigest, e := digestFile(filepath.Join(point, "manifest.json"))
	if e != nil {
		return e
	}
	restoredDigest, e := digestFile(filepath.Join(verification, "capture/manifest.json"))
	if e != nil {
		return e
	}
	if originalDigest != restoredDigest || recovered.Operation != m.Operation {
		return errors.New("publication verification differs from protected capture")
	}
	// Append-only publication: back up the same protected tree after read-back.
	// Restic deduplicates its data; this complete point is independently usable
	// and has no retention dependency on the candidate verification snapshot.
	completeID, e := w.backup(w.staging, "capture", "bartl-forgejo-complete")
	if e != nil {
		return e
	}
	completeRoot := filepath.Join(verification, "complete")
	if e = w.restoreSnapshot(completeID, completeRoot); e != nil {
		return e
	}
	completed, e := verifyPoint(filepath.Join(completeRoot, "capture"), w.id)
	if e != nil {
		return e
	}
	completeDigest, e := digestFile(filepath.Join(completeRoot, "capture/manifest.json"))
	if e != nil {
		return e
	}
	if completeDigest != originalDigest || completed.Operation != m.Operation {
		return errors.New("published complete point differs from protected capture")
	}
	receipt = publicationResult{state{w.id, w.version, w.operation}, completeID, originalDigest}
	if e = atomicJSON(receiptPath, receipt); e != nil {
		return e
	}
	return w.cleanupPublication(receipt)
}
