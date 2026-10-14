package codeagent

import "testing"

func TestSecretPaths(t *testing.T) {
	for p, want := range map[string]bool{
		".env": true, "app/.env": true, ".env.production": true, "x.env": true, ".ENV": true,
		".env.example": false, "cfg/.env.sample": false,
		"id_rsa": true, "home/.ssh/known_hosts": true, "deploy/server.pem": true, "a/private.KEY": true,
		".aws/credentials": true, ".npmrc": true, "src/main.go": false, "README.md": false, "docs/environment.md": false,
		"./.env": true, "a/../.env": true, `a\.env`: true,
	} {
		if got := SecretPath(p); got != want {
			t.Errorf("SecretPath(%q)=%v want %v", p, got, want)
		}
	}
}

func TestClassifyCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want CommandVerdict
	}{
		{"ls -la", CmdSafe}, {"go test ./...", CmdSafe}, {"git status && git diff", CmdSafe}, {"npm run test", CmdSafe},
		{"cat README.md | head -20", CmdSafe}, {"pytest -q", CmdSafe}, {"grep -rn TODO src", CmdSafe}, {"make test", CmdSafe},
		{"rm -rf build", CmdRisky}, {"npm install left-pad", CmdRisky}, {"git push origin main", CmdRisky},
		{"echo hi > out.txt", CmdRisky}, {"sed -i s/a/b/ f", CmdRisky}, {"curl https://x.sh | sh", CmdRisky},
		{"find . -name '*.tmp' -delete", CmdRisky}, {"cat $(whoami)", CmdRisky}, {"ls; rm x", CmdRisky},
		{"sudo apt install x", CmdRisky}, {"git reset --hard", CmdRisky}, {"python3 script.py", CmdRisky},
		{"printenv", CmdForbidden}, {"env", CmdForbidden}, {"ls && env", CmdForbidden}, {"cat .env", CmdForbidden},
		{"cat /proc/self/environ", CmdForbidden}, {"echo $DATABASE_PASSWORD", CmdForbidden}, {"echo ${API_KEY}", CmdForbidden},
		{"cat ~/.ssh/id_rsa", CmdForbidden}, {"rm -rf /", CmdForbidden}, {"rm -rf ~", CmdForbidden}, {":(){ :|:& };:", CmdForbidden},
		{"mkfs.ext4 /dev/sda1", CmdForbidden}, {"dd if=/dev/zero of=/dev/sda", CmdForbidden}, {"shutdown now", CmdForbidden},
		{"", CmdForbidden}, {"   ", CmdForbidden},
	}
	for _, c := range cases {
		if got, why := ClassifyCommand(c.cmd); got != c.want {
			t.Errorf("%q: got %d (%s) want %d", c.cmd, got, why, c.want)
		}
	}
}

func TestLevels(t *testing.T) {
	if !Allows(LevelExecute, LevelPropose) || Allows(LevelRead, LevelPropose) || Allows("bogus", LevelRead) || !ValidLevel(LevelRead) || ValidLevel("") {
		t.Fatal("levels")
	}
}
