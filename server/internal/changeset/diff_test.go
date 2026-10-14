package changeset

import (
	"math/rand"
	"strings"
	"testing"
)

func TestApplyAllAndNoneRoundTrip(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\np\n"
	after := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\nO\nP\nq\n"
	if got := Apply(before, after, nil); got != after {
		t.Fatalf("all: %q", got)
	}
	if got := Apply(before, after, map[int]bool{}); got != before {
		t.Fatalf("none: %q", got)
	}
}

func TestSeparateHunksCanBeChosenIndependently(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "line"+string(rune('a'+i%26)))
	}
	before := strings.Join(lines, "\n") + "\n"
	mod := append([]string{}, lines...)
	mod[2] = "CHANGED-EARLY"
	mod[35] = "CHANGED-LATE"
	after := strings.Join(mod, "\n") + "\n"
	hs := Hunks(before, after)
	if len(hs) != 2 {
		t.Fatalf("hunks: %d", len(hs))
	}
	only2 := Apply(before, after, map[int]bool{1: true})
	if !strings.Contains(only2, "CHANGED-LATE") || strings.Contains(only2, "CHANGED-EARLY") {
		t.Fatalf("second only: %q", only2)
	}
	only1 := Apply(before, after, map[int]bool{0: true})
	if strings.Contains(only1, "CHANGED-LATE") || !strings.Contains(only1, "CHANGED-EARLY") {
		t.Fatalf("first only: %q", only1)
	}
}

func TestHunkNumbersAndContext(t *testing.T) {
	hs := Hunks("one\ntwo\nthree\n", "one\n2\nthree\n")
	if len(hs) != 1 {
		t.Fatal(len(hs))
	}
	h := hs[0]
	if h.OldStart != 1 || h.OldLines != 3 || h.NewLines != 3 || h.Added != 1 || h.Removed != 1 {
		t.Fatalf("%+v", h)
	}
	if h.Lines[0].Kind != "context" || h.Lines[1].Kind != "del" || h.Lines[2].Kind != "add" {
		t.Fatalf("%+v", h.Lines)
	}
}

func TestEdgeCases(t *testing.T) {
	cases := [][2]string{{"", ""}, {"", "new\n"}, {"old\n", ""}, {"no newline", "no newline\nplus"}, {"a\r\nb\r\n", "a\r\nc\r\n"}, {"x", "x"}}
	for _, c := range cases {
		if got := Apply(c[0], c[1], nil); got != c[1] {
			t.Fatalf("%q -> %q gave %q", c[0], c[1], got)
		}
		if got := Apply(c[0], c[1], map[int]bool{}); got != c[0] {
			t.Fatalf("none %q: %q", c[0], got)
		}
	}
	if len(Hunks("same\n", "same\n")) != 0 {
		t.Fatal("identical text has no hunks")
	}
}

func TestRandomEditsRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for n := 0; n < 200; n++ {
		var a []string
		for i := 0; i < r.Intn(60); i++ {
			a = append(a, string(rune('a'+r.Intn(5)))+"\n")
		}
		b := append([]string{}, a...)
		for k := 0; k < r.Intn(8); k++ {
			switch r.Intn(3) {
			case 0:
				if len(b) > 0 {
					i := r.Intn(len(b))
					b = append(b[:i], b[i+1:]...)
				}
			case 1:
				i := r.Intn(len(b) + 1)
				b = append(b[:i], append([]string{"N" + string(rune('a'+r.Intn(5))) + "\n"}, b[i:]...)...)
			case 2:
				if len(b) > 0 {
					b[r.Intn(len(b))] = "M\n"
				}
			}
		}
		sa, sb := strings.Join(a, ""), strings.Join(b, "")
		if Apply(sa, sb, nil) != sb || Apply(sa, sb, map[int]bool{}) != sa {
			t.Fatalf("round trip failed for %q -> %q", sa, sb)
		}
		// Any subset yields text between the two: each chosen hunk applied, others not.
		hs := Hunks(sa, sb)
		pick := map[int]bool{}
		for _, h := range hs {
			if r.Intn(2) == 0 {
				pick[h.ID] = true
			}
		}
		_ = Apply(sa, sb, pick)
	}
}

func TestHugeInputsDegradeToWholeFileHunk(t *testing.T) {
	a := strings.Repeat("x\n", 5000)
	b := strings.Repeat("y\n", 5000)
	if got := Apply(a, b, nil); got != b {
		t.Fatal("big replace")
	}
	if got := Apply(a, b, map[int]bool{}); got != a {
		t.Fatal("big none")
	}
}
