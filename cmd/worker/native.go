// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func (w *worker) nativeEnv() []string {
	home := filepath.Join(w.data, "home")
	// Explicit environment prevents backup credentials and caller Git overrides
	// reaching Forgejo, its hooks or its child processes.
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + home, "USER=git", "LOGNAME=git", "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(home, ".gitconfig"), "GIT_TERMINAL_PROMPT=0", "TMPDIR=/tmp"}
}
func (w *worker) nativeCommand(version, config string, args ...string) *exec.Cmd {
	all := append([]string{"--work-path", w.data, "--custom-path", filepath.Join(w.data, "custom"), "--config", config}, args...)
	command := exec.Command("/usr/local/lib/forgejo/"+version+"/forgejo", all...)
	command.Dir = w.data
	command.Env = w.nativeEnv()
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command
}
func (w *worker) nativeCycle(version string, c ini, flushBeforeUpgrade bool) error {
	// A finite isolated startup performs native initialization, queue flushing,
	// or migration. No service selects this Job; native listeners are loopback.
	c.set("server", "HTTP_ADDR", "127.0.0.1")
	c.set("server", "SSH_LISTEN_HOST", "127.0.0.1")
	f, e := os.CreateTemp("", "forgejo-isolated-*.ini")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(c.bytes())
	}
	f.Close()
	if e != nil {
		return e
	}
	cmd := w.nativeCommand(version, name, "web")
	if e = cmd.Start(); e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	stop := func() error {
		if e := cmd.Process.Signal(syscall.SIGTERM); e != nil {
			return e
		}
		select {
		case e := <-done:
			return e
		case <-time.After(60 * time.Second):
			cmd.Process.Kill()
			<-done
			return errors.New("native graceful stop timed out; mutation remains failed")
		}
	}
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(180 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case e := <-done:
			if e == nil {
				e = errors.New("native process exited before readiness")
			}
			return fmt.Errorf("native %s startup: %w", version, e)
		case <-signals:
			stop()
			return errors.New("native preparation interrupted")
		case <-deadline.C:
			stop()
			return errors.New("native readiness timeout")
		case <-ticker.C:
			response, e := client.Get("http://127.0.0.1:3000/api/healthz")
			if e != nil {
				continue
			}
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				if flushBeforeUpgrade {
					flush := w.nativeCommand(version, name, "manager", "flush-queues", "--timeout", "60s")
					if e = flush.Run(); e != nil {
						stop()
						return fmt.Errorf("native pre-upgrade queue flush: %w", e)
					}
				}
				if e = stop(); e != nil {
					return e
				}
				// Native startup may persist generated signing settings. Retain them in the
				// owned configuration rather than losing them with the isolated listener.
				saved, e := loadINI(name)
				if e != nil {
					return e
				}
				for section, settings := range saved {
					for key, value := range settings {
						c.set(section, key, value)
					}
				}
				return nil
			}
		}
	}
}
func (w *worker) prepare() error {
	if present, e := exists(w.mutationPath()); e != nil {
		return e
	} else if present {
		return errors.New("previous state mutation did not complete; use independent empty state")
	}
	_, e := os.Stat(w.statePath())
	fresh := errors.Is(e, os.ErrNotExist)
	if e != nil && !fresh {
		return e
	}
	var previous state
	if fresh {
		// Only the package's own lock directory and filesystem-created lost+found
		// are permitted on empty state. A stray native DB is never initialization.
		entries, e := os.ReadDir(w.data)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if entry.Name() != ".bartl" && entry.Name() != "lost+found" {
				return errors.New("unowned or incomplete state exists; initialization refused")
			}
		}
		if e = w.beginMutation("initialize-or-restore"); e != nil {
			return e
		}
		restored, e := w.restore()
		if e != nil {
			return e
		}
		if restored {
			if e = readJSON(w.statePath(), &previous); e != nil {
				return e
			}
			if previous.identity != w.id || !supported(previous.Version) {
				return errors.New("restored identity or version is unsupported")
			}
			fresh = false
		}
	} else {
		previous, e = w.cleanState()
		if e != nil {
			return e
		}
	}
	// Refuse unsupported/downgrade before touching native state or configuration.
	if !fresh && previous.Version != w.version && !(previous.Version == "16.0.7" && w.version == "17.0.1") {
		return errors.New("unsupported transition or downgrade refused")
	}
	c, e := w.configuration(fresh)
	if e != nil {
		return e
	}
	if e = w.beginMutation("native-prepare"); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Join(w.data, "home"), 0700); e != nil {
		return e
	}
	// Keep the recovery identity in owned storage, separately from target input.
	if e = atomicFile(filepath.Join(w.data, "app.ini"), c.bytes(), 0600); e != nil {
		return e
	}
	if !fresh && previous.Version != w.version {
		if e = w.nativeCycle(previous.Version, c, true); e != nil {
			return e
		}
	}
	if e = w.nativeCycle(w.version, c, false); e != nil {
		return e
	}
	c.set("server", "HTTP_ADDR", "0.0.0.0")
	c.set("server", "SSH_LISTEN_HOST", "0.0.0.0")
	if e = atomicFile(filepath.Join(w.data, "app.ini"), c.bytes(), 0600); e != nil {
		return e
	}
	if fresh {
		username, e := os.ReadFile("/bootstrap/username")
		if e == nil {
			password, e := os.ReadFile("/bootstrap/password")
			if e != nil {
				return e
			}
			email, e := os.ReadFile("/bootstrap/email")
			if e != nil {
				return e
			}
			command := w.nativeCommand(w.version, filepath.Join(w.data, "app.ini"), "admin", "user", "create", "--username", stringTrim(username), "--password", stringTrim(password), "--email", stringTrim(email), "--admin", "--must-change-password=false")
			if e = command.Run(); e != nil {
				return fmt.Errorf("native admin bootstrap: %w", e)
			}
		} else if !errors.Is(e, os.ErrNotExist) || os.Getenv("BOOTSTRAP_CONFIGURED") == "true" {
			return fmt.Errorf("configured bootstrap username is unavailable: %w", e)
		}
	}
	if e = w.confirmShutdown(); e != nil {
		return e
	}
	return w.commitState()
}
func (w *worker) serve() error {
	// Register before any native start or state change so termination during
	// startup cannot bypass the durable uncertainty marker.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	s, e := w.cleanState()
	if e != nil {
		return e
	}
	if s.Version != w.version {
		return errors.New("requested version has not completed prepare")
	}
	c, e := w.configuration(false)
	if e != nil {
		return e
	}
	// An ordinary restart never restores or performs a separate preparation.
	if e = atomicFile(filepath.Join(w.data, "app.ini"), c.bytes(), 0600); e != nil {
		return e
	}
	return w.serveNative(w.nativeCommand(w.version, filepath.Join(w.data, "app.ini"), "web"), signals, 60*time.Second)
}

func (w *worker) serveNative(cmd *exec.Cmd, signals <-chan os.Signal, stopTimeout time.Duration) error {
	// Unlike interrupted preparation this marker does not prohibit a native
	// restart. Only a successful native startup and graceful end clear it.
	if e := atomicJSON(w.shutdownPath(), w.id); e != nil {
		return e
	}
	if e := cmd.Start(); e != nil {
		return e
	}
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	readiness := ticker.C
	ready := false
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var e error
	for {
		select {
		case e := <-done:
			if e == nil {
				e = errors.New("native process exited without requested graceful shutdown")
			}
			return e
		case <-readiness:
			response, err := client.Get("http://127.0.0.1:3000/api/healthz")
			if err == nil {
				ready = response.StatusCode == http.StatusOK
				response.Body.Close()
				if ready {
					ticker.Stop()
					readiness = nil
				}
			}
		case s := <-signals:
			// A process that already died must never be mistaken for a
			// successful requested stop when signal and Wait race.
			select {
			case e := <-done:
				if e == nil {
					e = errors.New("native process exited before requested graceful shutdown")
				}
				return e
			default:
			}
			if e = cmd.Process.Signal(s); e != nil {
				return e
			}
			goto stopping
		}
	}
stopping:
	select {
	case e := <-done:
		if e != nil {
			return e
		}
		if !ready {
			return errors.New("native stopped before successful startup was confirmed")
		}
		return w.confirmShutdown()
	case <-time.After(stopTimeout):
		cmd.Process.Kill()
		<-done
		return errors.New("native graceful stop timed out")
	}
}
