// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The supported profile accepts ordinary single-line native INI settings. It
// does not interpolate deployment input or interpret application schema.
type ini map[string]map[string]string

func loadINI(path string) (ini, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	c := ini{"": {}}
	section := ""
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if c[section] == nil {
				c[section] = map[string]string{}
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("configuration requires single-line native INI settings")
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" || strings.Contains(value, "\"\"\"") {
			return nil, errors.New("unsupported multiline INI setting")
		}
		if _, duplicate := c[section][key]; duplicate {
			return nil, fmt.Errorf("duplicate configuration key [%s] %s", section, key)
		}
		c[section][key] = value
	}
	return c, scan.Err()
}
func (c ini) set(section, key, value string) {
	if c[section] == nil {
		c[section] = map[string]string{}
	}
	c[section][key] = value
}
func (c ini) bytes() []byte {
	var b strings.Builder
	sections := make([]string, 0, len(c))
	for s := range c {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	for _, s := range sections {
		if s != "" {
			fmt.Fprintf(&b, "\n[%s]\n", s)
		}
		keys := make([]string, 0, len(c[s]))
		for k := range c[s] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", k, c[s][k])
		}
	}
	return []byte(b.String())
}
func protected(section, key string) bool {
	return section == "security" && (strings.Contains(key, "SECRET") || strings.Contains(key, "TOKEN")) || section == "server" && strings.HasPrefix(key, "LFS_JWT_") || section == "oauth2" && (strings.Contains(key, "SECRET") || strings.Contains(key, "SIGNING_"))
}
func (w *worker) configuration(fresh bool) (ini, error) {
	target, e := loadINI(w.config)
	if e != nil {
		return nil, e
	}
	c := ini{"": {}}
	if !fresh {
		c, e = loadINI(filepath.Join(w.data, "app.ini"))
		if e != nil {
			return nil, e
		}
	}
	for section, settings := range target {
		for key, value := range settings {
			if !fresh && protected(section, key) {
				continue
			}
			c.set(section, key, value)
		}
	}
	// All owned native persistence is relative to this target, never to a source
	// installation or externally owned database/object store.
	for section, settings := range c {
		for key, value := range settings {
			raw := strings.Trim(value, "`\"")
			if key == "STORAGE_TYPE" && raw != "" && raw != "local" {
				return nil, fmt.Errorf("[%s] nonlocal storage is outside the supported profile", section)
			}
			if strings.HasSuffix(key, "_URI") && (strings.Contains(key, "SECRET") || strings.Contains(key, "TOKEN")) && raw != "" {
				uri, e := url.Parse(raw)
				if e != nil || uri.Scheme != "file" || uri.Host != "" {
					return nil, errors.New("native secret URI must be a local owned file")
				}
				path := uri.Path
				if !filepath.IsAbs(path) {
					path = filepath.Join(w.data, "data", path)
				}
				if !strings.HasPrefix(filepath.Clean(path), w.data+"/") {
					return nil, errors.New("secret file URI must belong to owned data")
				}
			}
		}
	}
	c.set("", "RUN_USER", "git")
	c.set("", "RUN_MODE", "prod")
	c.set("", "WORK_PATH", w.data)
	c.set("server", "APP_DATA_PATH", filepath.Join(w.data, "data"))
	c.set("server", "HTTP_ADDR", "0.0.0.0")
	c.set("server", "HTTP_PORT", "3000")
	c.set("server", "PROTOCOL", "http")
	c.set("server", "LOCAL_ROOT_URL", "http://127.0.0.1:3000/")
	c.set("server", "START_SSH_SERVER", "true")
	c.set("server", "SSH_LISTEN_HOST", "0.0.0.0")
	c.set("server", "SSH_LISTEN_PORT", "2222")
	c.set("server", "SSH_ROOT_PATH", filepath.Join(w.data, "data/ssh"))
	c.set("server", "SSH_SERVER_HOST_KEYS", "ssh/forgejo.ed25519")
	c.set("server", "SSH_USER", "git")
	c.set("server", "BUILTIN_SSH_SERVER_USER", "git")
	c.set("server", "LFS_START_SERVER", "true")
	c.set("server", "OFFLINE_MODE", "true")
	c.set("database", "DB_TYPE", "sqlite3")
	c.set("database", "PATH", filepath.Join(w.data, "data/forgejo.db"))
	c.set("repository", "ROOT", filepath.Join(w.data, "data/repositories"))
	c.set("repository", "DISABLE_MIGRATIONS", "true")
	c.set("security", "INSTALL_LOCK", "true")
	c.set("security", "DISABLE_GIT_HOOKS", "true")
	c.set("security", "DISABLE_WEBHOOKS", "true")
	c.set("service", "DISABLE_REGISTRATION", "true")
	c.set("service", "ENABLE_NOTIFY_MAIL", "false")
	for _, section := range []string{"mailer", "email.incoming", "actions", "mirror", "federation", "cron.update_mirrors", "cron.sync_external_users", "cron.update_checker"} {
		c.set(section, "ENABLED", "false")
	}
	// Disable delivery natively, including recovered queued webhook tasks.
	c.set("webhook", "ALLOWED_HOST_LIST", "loopback")
	c.set("log", "MODE", "console")
	c.set("log", "ROOT_PATH", filepath.Join(w.data, "log"))
	for section, relative := range map[string]string{"attachment": "data/attachments", "lfs": "data/lfs", "avatar": "data/avatars", "repo-avatar": "data/repo-avatars", "repo-archive": "data/repo-archive", "packages": "data/packages"} {
		c.set(section, "STORAGE_TYPE", "local")
		c.set(section, "PATH", filepath.Join(w.data, relative))
	}
	// Native sections may carry additional file paths. Reject paths outside this
	// volume instead of silently referring back to a recovered source.
	for section, settings := range c {
		for key, value := range settings {
			if key == "PATH" || key == "ROOT" || strings.HasSuffix(key, "_PATH") || strings.HasSuffix(key, "_FILE") {
				raw := strings.Trim(value, "`\"")
				if filepath.IsAbs(raw) && raw != w.data && !strings.HasPrefix(filepath.Clean(raw), w.data+"/") {
					return nil, fmt.Errorf("[%s] %s must remain within owned state", section, key)
				}
			}
		}
	}
	if fresh {
		for _, pair := range [][2]string{{"security", "SECRET_KEY"}, {"security", "INTERNAL_TOKEN"}, {"server", "LFS_JWT_SECRET"}} {
			if c[pair[0]][pair[1]] == "" && c[pair[0]][pair[1]+"_URI"] == "" {
				secret := make([]byte, 32)
				if _, e = rand.Read(secret); e != nil {
					return nil, e
				}
				c.set(pair[0], pair[1], base64.RawURLEncoding.EncodeToString(secret))
			}
		}
	}
	return c, nil
}
