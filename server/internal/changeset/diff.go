package changeset

import "strings"

// Line-based diff used to show a proposed change and to apply part of it.
//
// Lines keep their terminators, so applying every hunk reproduces the proposed
// text byte for byte and applying none reproduces the original.

// DiffLine is one line of a hunk.
type DiffLine struct {
	Kind string `json:"kind"` // context | add | del
	Text string `json:"text"` // without the line terminator
}

// Hunk is one contiguous group of changes with its surrounding context.
type Hunk struct {
	ID       int        `json:"id"`
	OldStart int        `json:"oldStart"` // 1-based line in the original
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Added    int        `json:"added"`
	Removed  int        `json:"removed"`
	Lines    []DiffLine `json:"lines"`
}

const contextLines = 3

// maxDiffCells bounds the work: bigger inputs fall back to one whole-file hunk.
const maxDiffCells = 16_000_000

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.SplitAfter(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

type opKind byte

const (
	opEq opKind = iota
	opDel
	opAdd
)

type op struct {
	kind opKind
	text string
}

// script computes an edit script from a to b.
func script(a, b []string) []op {
	// Trim the common prefix and suffix; most edits are local.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	var mid []op
	switch {
	case len(ma) == 0:
		for _, l := range mb {
			mid = append(mid, op{opAdd, l})
		}
	case len(mb) == 0:
		for _, l := range ma {
			mid = append(mid, op{opDel, l})
		}
	case len(ma)*len(mb) > maxDiffCells:
		for _, l := range ma {
			mid = append(mid, op{opDel, l})
		}
		for _, l := range mb {
			mid = append(mid, op{opAdd, l})
		}
	default:
		mid = lcsScript(ma, mb)
	}
	out := make([]op, 0, pre+len(mid)+suf)
	for _, l := range a[:pre] {
		out = append(out, op{opEq, l})
	}
	out = append(out, mid...)
	for _, l := range a[len(a)-suf:] {
		out = append(out, op{opEq, l})
	}
	return out
}

func lcsScript(a, b []string) []op {
	n, m := len(a), len(b)
	w := m + 1
	t := make([]uint16, (n+1)*w)
	// Lengths can exceed 65535 only for enormous inputs, which the cell bound excludes.
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i*w+j] = t[(i+1)*w+j+1] + 1
			} else if t[(i+1)*w+j] >= t[i*w+j+1] {
				t[i*w+j] = t[(i+1)*w+j]
			} else {
				t[i*w+j] = t[i*w+j+1]
			}
		}
	}
	var out []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, op{opEq, a[i]})
			i++
			j++
		case t[(i+1)*w+j] >= t[i*w+j+1]:
			out = append(out, op{opDel, a[i]})
			i++
		default:
			out = append(out, op{opAdd, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, op{opDel, a[i]})
	}
	for ; j < m; j++ {
		out = append(out, op{opAdd, b[j]})
	}
	return out
}

// group is a run of changes; ops[start:end] are all del/add except interior equal runs.
type group struct{ start, end int }

// groups merges changes separated by no more than two context windows so
// hunks never overlap when shown.
func groups(ops []op) []group {
	var gs []group
	i := 0
	for i < len(ops) {
		if ops[i].kind == opEq {
			i++
			continue
		}
		start := i
		end := i
		for end < len(ops) {
			if ops[end].kind != opEq {
				end++
				continue
			}
			// count the equal run
			run := end
			for run < len(ops) && ops[run].kind == opEq {
				run++
			}
			if run < len(ops) && run-end <= 2*contextLines {
				end = run
				continue
			}
			break
		}
		gs = append(gs, group{start, end})
		i = end
	}
	return gs
}

// Hunks returns the hunks that turn before into after.
func Hunks(before, after string) []Hunk {
	a, b := splitLines(before), splitLines(after)
	ops := script(a, b)
	gs := groups(ops)
	// Positions in a and b at each op index.
	oldAt := make([]int, len(ops)+1)
	newAt := make([]int, len(ops)+1)
	for i, o := range ops {
		oldAt[i+1], newAt[i+1] = oldAt[i], newAt[i]
		if o.kind != opAdd {
			oldAt[i+1]++
		}
		if o.kind != opDel {
			newAt[i+1]++
		}
	}
	trim := func(s string) string { return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r") }
	var hs []Hunk
	for id, g := range gs {
		from := g.start
		for c := 0; c < contextLines && from > 0 && ops[from-1].kind == opEq; c++ {
			from--
		}
		to := g.end
		for c := 0; c < contextLines && to < len(ops) && ops[to].kind == opEq; c++ {
			to++
		}
		h := Hunk{ID: id, OldStart: oldAt[from] + 1, NewStart: newAt[from] + 1, OldLines: oldAt[to] - oldAt[from], NewLines: newAt[to] - newAt[from]}
		for _, o := range ops[from:to] {
			switch o.kind {
			case opEq:
				h.Lines = append(h.Lines, DiffLine{"context", trim(o.text)})
			case opDel:
				h.Removed++
				h.Lines = append(h.Lines, DiffLine{"del", trim(o.text)})
			case opAdd:
				h.Added++
				h.Lines = append(h.Lines, DiffLine{"add", trim(o.text)})
			}
		}
		hs = append(hs, h)
	}
	return hs
}

// Apply builds the text with only the chosen hunks applied to before. A nil
// choose applies every hunk. Hunk ids come from Hunks(before, after).
func Apply(before, after string, choose map[int]bool) string {
	a, b := splitLines(before), splitLines(after)
	ops := script(a, b)
	gs := groups(ops)
	var sb strings.Builder
	pos := 0
	for id, g := range gs {
		for ; pos < g.start; pos++ {
			sb.WriteString(ops[pos].text)
		}
		take := choose == nil || choose[id]
		for ; pos < g.end; pos++ {
			o := ops[pos]
			switch o.kind {
			case opEq:
				sb.WriteString(o.text)
			case opDel:
				if !take {
					sb.WriteString(o.text)
				}
			case opAdd:
				if take {
					sb.WriteString(o.text)
				}
			}
		}
	}
	for ; pos < len(ops); pos++ {
		sb.WriteString(ops[pos].text)
	}
	return sb.String()
}

// Stats counts added and removed lines.
func Stats(before, after string) (added, removed int) {
	for _, o := range script(splitLines(before), splitLines(after)) {
		switch o.kind {
		case opAdd:
			added++
		case opDel:
			removed++
		}
	}
	return
}
