// SPDX-License-Identifier: Apache-2.0
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

type member struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Link   string `json:"link,omitempty"`
}
type manifest struct {
	state
	Format   int       `json:"format"`
	Captured time.Time `json:"captured"`
	Members  []member  `json:"members"`
}

func stringTrim(b []byte) string { return strings.TrimSpace(string(b)) }
func skipNative(relative string) bool {
	return relative == "lost+found" || relative == ".bartl/lock" || relative == ".bartl/mutation.json" || relative == ".bartl/native-running.json" || strings.HasPrefix(relative, ".bartl/restore") || strings.HasPrefix(relative, ".bartl/.write-")
}
func inventory(root string) ([]member, error) {
	var members []member
	e := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if relative == "." {
			return nil
		}
		if skipNative(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		m := member{Path: filepath.ToSlash(relative), Mode: uint32(info.Mode())}
		switch {
		case info.IsDir():
		case info.Mode()&os.ModeSymlink != 0:
			target, e := os.Readlink(path)
			if e != nil {
				return e
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(path), target))
			if filepath.IsAbs(target) || !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
				return fmt.Errorf("state symlink %s escapes owned storage", relative)
			}
			m.Link = target
		case info.Mode().IsRegular():
			f, e := os.Open(path)
			if e != nil {
				return e
			}
			hash := sha256.New()
			size, e := io.Copy(hash, f)
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			m.Size = size
			m.SHA256 = hex.EncodeToString(hash.Sum(nil))
		default:
			return fmt.Errorf("unsupported native filesystem member %s", relative)
		}
		members = append(members, m)
		return nil
	})
	return members, e
}
func copyTree(source, target string) error {
	type directory struct {
		path string
		mode os.FileMode
	}
	var directories []directory
	e := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		if relative == "." {
			// The mount root is operator-owned and excluded from inventory.
			// fsGroup may permit writes without permitting chmod of that root.
			return os.MkdirAll(target, 0700)
		}
		if skipNative(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			directories = append(directories, directory{destination, info.Mode()})
			if e = os.MkdirAll(destination, 0700); e != nil {
				return e
			}
			return os.Chmod(destination, 0700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, e := os.Readlink(path)
			if e != nil {
				return e
			}
			return os.Symlink(link, destination)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported filesystem member %s", relative)
		}
		input, e := os.Open(path)
		if e != nil {
			return e
		}
		defer input.Close()
		output, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if e != nil {
			return e
		}
		_, e = io.Copy(output, input)
		if e == nil {
			e = output.Chmod(info.Mode())
		}
		if e == nil {
			e = output.Sync()
		}
		closeErr := output.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
		return os.Chtimes(destination, info.ModTime(), info.ModTime())
	})
	if e != nil {
		return e
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if e = os.Chmod(directories[i].path, directories[i].mode); e != nil {
			return e
		}
		if e = syncDir(directories[i].path); e != nil {
			return e
		}
	}
	return syncDir(target)
}
func verifyPoint(root string, id identity) (manifest, error) {
	var m manifest
	if e := readJSON(filepath.Join(root, "manifest.json"), &m); e != nil {
		return m, e
	}
	if m.Format != 1 || m.identity != id || !supported(m.Version) || m.Operation == "" || len(m.Members) == 0 {
		return m, errors.New("invalid recovery point metadata")
	}
	actual, e := inventory(filepath.Join(root, "state"))
	if e != nil {
		return m, e
	}
	if !reflect.DeepEqual(actual, m.Members) {
		return m, errors.New("recovery point member inventory or checksum mismatch")
	}
	var s state
	if e = readJSON(filepath.Join(root, "state/.bartl/state.json"), &s); e != nil {
		return m, e
	}
	if s.identity != m.identity || s.Version != m.Version {
		return m, errors.New("recovery point state metadata mismatch")
	}
	present, e := exists(filepath.Join(root, "state/data/forgejo.db"))
	if e != nil {
		return m, e
	}
	if !present {
		return m, errors.New("recovery point native database is missing")
	}
	return m, nil
}
func (w *worker) capture() error {
	if os.Getenv("SOURCE_STOPPED") != "true" {
		return errors.New("capture requires controller-confirmed stopped application Pods")
	}
	s, e := w.cleanState()
	if e != nil {
		return e
	}
	if s.Version != w.version {
		return errors.New("capture version does not match state")
	}
	// Committed owned state remains eligible for the ordinary native restart.
	// Unconfirmed graceful shutdown blocks capture, not that restart.
	w.result.SourceIntact = true
	if pending, e := exists(w.shutdownPath()); e != nil {
		return e
	} else if pending {
		return errors.New("native graceful shutdown is unconfirmed; restart natively and stop gracefully before capture")
	}
	// Serialize staging work only after establishing the source's condition.
	// Failure to obtain a staging lock cannot damage the verified source.
	stagingLock, e := os.OpenFile(filepath.Join(w.staging, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer stagingLock.Close()
	if e = syscall.Flock(int(stagingLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errors.New("conflicting worker holds staging lock")
	}
	defer syscall.Flock(int(stagingLock.Fd()), syscall.LOCK_UN)
	final := filepath.Join(w.staging, "capture")
	if present, e := exists(final); e != nil {
		return e
	} else if present {
		return errors.New("protected capture already exists; refusing overwrite")
	}
	// The atomic rename to capture is the protection boundary. An owned,
	// uncommitted copy may be discarded only after independently verifying the
	// current source; unknown or foreign staging is never adopted or removed.
	before, e := inventory(w.data)
	if e != nil {
		return e
	}
	partial := filepath.Join(w.staging, "capture-in-progress")
	if info, err := os.Lstat(partial); err == nil {
		if !info.IsDir() {
			return errors.New("partial capture is not an owned staging directory")
		}
		manifestPath := filepath.Join(partial, "manifest.json")
		info, err = os.Lstat(manifestPath)
		if err != nil {
			return fmt.Errorf("partial capture ownership is unknown: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("partial capture ownership manifest is not a regular file")
		}
		var previous manifest
		if err = readJSON(manifestPath, &previous); err != nil {
			return fmt.Errorf("partial capture ownership is unknown: %w", err)
		}
		if previous.identity != w.id || previous.Version != w.version || previous.Operation == "" || previous.Format != 1 {
			return errors.New("partial capture does not belong to this application and native version")
		}
		if e = os.RemoveAll(partial); e != nil {
			return e
		}
		if e = syncDir(w.staging); e != nil {
			return e
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if e = os.Mkdir(partial, 0700); e != nil {
		return e
	}
	pointState := state{w.id, w.version, w.operation}
	if e = atomicJSON(filepath.Join(partial, "manifest.json"), manifest{pointState, 1, time.Now().UTC(), before}); e != nil {
		return e
	}
	if e = copyTree(w.data, filepath.Join(partial, "state")); e != nil {
		return e
	}
	if _, e = verifyPoint(partial, w.id); e != nil {
		return e
	}
	if e = syncDir(partial); e != nil {
		return e
	}
	if e = os.Rename(partial, final); e != nil {
		return e
	}
	if e = syncDir(w.staging); e != nil {
		return e
	}
	w.result.Capture = w.operation
	return nil
}
