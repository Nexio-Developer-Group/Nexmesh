// Package ports detects TCP ports listening inside the workspace by reading
// /proc, so previews can be offered for whatever the developer starts.
package ports

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

type Port struct {
	Port    int    `json:"port"`
	Local   bool   `json:"local"` // bound to loopback only
	Process string `json:"process,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

type Scanner struct {
	Proc string // "/proc" unless testing
	// Ignore lists ports never reported (the agent's own).
	Ignore map[int]bool
}

func NewScanner() *Scanner { return &Scanner{Proc: "/proc", Ignore: map[int]bool{}} }

// Scan returns listening TCP ports, sorted, deduplicated per port.
func (s *Scanner) Scan() []Port {
	inodes := map[string]int{} // socket inode -> port, filled below
	byPort := map[int]*Port{}
	for _, f := range []string{"net/tcp", "net/tcp6"} {
		fh, err := os.Open(filepath.Join(s.Proc, f))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Scan() // header
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 || fields[3] != "0A" { // LISTEN
				continue
			}
			addr, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			pn, err := strconv.ParseInt(portHex, 16, 32)
			if err != nil || pn == 0 || s.Ignore[int(pn)] {
				continue
			}
			local := isLoopback(addr)
			p := byPort[int(pn)]
			if p == nil {
				p = &Port{Port: int(pn), Local: local}
				byPort[int(pn)] = p
			} else if !local {
				p.Local = false // reachable on any interface wins
			}
			inodes[fields[9]] = int(pn)
		}
		fh.Close()
	}
	s.attachProcesses(inodes, byPort)
	out := make([]Port, 0, len(byPort))
	for _, p := range byPort {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func isLoopback(hexAddr string) bool {
	switch hexAddr {
	case "0100007F", "00000000000000000000000001000000":
		return true
	}
	// IPv4-mapped loopback: ::ffff:127.0.0.1
	return strings.HasPrefix(hexAddr, "0000000000000000FFFF0000") && strings.HasSuffix(hexAddr, "0100007F")
}

// attachProcesses is best effort: it needs permission to read other
// processes' fd tables, which the workspace user has for its own processes.
func (s *Scanner) attachProcesses(inodes map[string]int, byPort map[int]*Port) {
	if len(inodes) == 0 {
		return
	}
	pids, _ := filepath.Glob(filepath.Join(s.Proc, "[0-9]*"))
	for _, dir := range pids {
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			l, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(l, "socket:[") {
				continue
			}
			port, ok := inodes[strings.TrimSuffix(strings.TrimPrefix(l, "socket:["), "]")]
			if !ok || byPort[port].PID != 0 {
				continue
			}
			pid, _ := strconv.Atoi(filepath.Base(dir))
			byPort[port].PID = pid
			if b, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
				byPort[port].Process = strings.TrimSpace(string(b))
			}
		}
	}
}

// Watch polls and calls emit with the full list whenever it changes (and once
// at start). It returns when ctx ends.
func (s *Scanner) Watch(ctx context.Context, every time.Duration, emit func([]Port)) {
	var last []Port
	first := true
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		cur := s.Scan()
		if first || !reflect.DeepEqual(cur, last) {
			first = false
			last = cur
			emit(cur)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type installer interface {
	Handle(method string, h agentproto.Handler)
}

func (s *Scanner) Install(a installer) {
	a.Handle("ports.list", func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"ports": s.Scan()}, nil
	})
}
