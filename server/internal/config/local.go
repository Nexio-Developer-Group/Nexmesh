package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// DataDir returns the per-user directory local mode keeps its data in.
func DataDir() string {
	if d := os.Getenv("NEXMESH_DATA_DIR"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Nexmesh")
		}
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, "Library", "Application Support", "Nexmesh")
		}
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "nexmesh")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "nexmesh")
}

// ApplyLocal sets defaults for the single-user, single-machine "local" mode
// (the double-click install): the simulated runtime, a file store, no sign-in
// and a loopback-only listener. Anything already set in the environment wins.
// It returns the URL to open.
func ApplyLocal() (url string, err error) {
	dir := DataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data directory %s: %w", dir, err)
	}
	port := 0
	for p := 8080; p < 8100; p++ {
		l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if e == nil {
			_ = l.Close()
			port = p
			break
		}
	}
	if port == 0 {
		return "", fmt.Errorf("ports 8080 to 8099 are all in use")
	}
	def := func(k, v string) {
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	def("NEXMESH_DATA_DIR", dir)
	def("NEXMESH_STORE", "file")
	def("NEXMESH_DRIVER", "fake")
	def("NEXMESH_AUTH", "disabled")
	def("NEXMESH_ADDR", fmt.Sprintf("127.0.0.1:%d", port))
	def("NEXMESH_PUBLIC_URL", fmt.Sprintf("http://127.0.0.1:%d", port))
	def("NEXMESH_FAKE_WORKSPACE_DIR", filepath.Join(dir, "workspaces"))
	def("NEXMESH_MASTER_KEY_FILE", filepath.Join(dir, "master.key"))
	if a := os.Getenv("NEXMESH_ADDR"); a != "" {
		if _, p, e := net.SplitHostPort(a); e == nil {
			port = atoi(p, port)
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port), nil
}

func atoi(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return def
	}
	return n
}
