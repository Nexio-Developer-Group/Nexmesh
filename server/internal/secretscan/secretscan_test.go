package secretscan

import (
	"strings"
	"testing"
)

func TestScrubRemovesCredentialShapedText(t *testing.T) {
	in := strings.Join([]string{
		"connecting to postgres://app:S3cr3tPw@db:5432/app",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123",
		"AWS_KEY=AKIAIOSFODNN7ABCDEFG",
		"password = hunter2hunter2",
		"DB_PASSWORD=supersecret",
		"token: ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEow...\n-----END RSA PRIVATE KEY-----",
		"sk-ant-api03-abcdefghijklmnopqrstuv",
		"plain log line: listening on port 8080, user count 12",
	}, "\n")
	out, n := Scrub(in)
	for _, bad := range []string{"S3cr3tPw", "abcdefghijklmnopqrstuvwxyz0123", "AKIAIOSFODNN7ABCDEFG", "hunter2hunter2", "supersecret", "ghp_abc", "eyJhbGci", "MIIEow", "sk-ant-api03"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q survived:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "listening on port 8080, user count 12") {
		t.Fatalf("scrubbing damaged ordinary text:\n%s", out)
	}
	if n < 8 {
		t.Fatalf("redactions: %d", n)
	}
}

func TestFindIsStricterThanScrub(t *testing.T) {
	if _, _, ok := Find("password = os.environ['PASSWORD']"); ok {
		t.Fatal("reading a secret from the environment is not a finding")
	}
	if _, _, ok := Find("password = 'hunter2hunter2'"); !ok {
		t.Fatal("hard-coded password must be found")
	}
}
