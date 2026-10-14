package envvars_test

import (
	"strings"
	"testing"

	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
)

func TestParseDotenv(t *testing.T) {
	src := "# comment\n\nexport A=1\nB = two words # trailing\nC=\"line1\\nline2 \\\"q\\\"\"\nD='lit\\n$x'\nE=\"multi\nline\"\nF=\nA=override\n1BAD=x\nNOEQUALS\nG=\"unterminated\n"
	vars, errs := envvars.ParseDotenv(src)
	got := map[string]string{}
	for _, v := range vars {
		got[v.Name] = v.Value
	}
	want := map[string]string{"A": "override", "B": "two words", "C": "line1\nline2 \"q\"", "D": `lit\n$x`, "E": "multi\nline", "F": ""}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(vars) != len(want) {
		t.Errorf("parsed %d vars: %+v", len(vars), vars)
	}
	if len(errs) != 3 {
		t.Fatalf("errors: %+v", errs)
	}
	if errs[0].Line != 11 || errs[1].Line != 12 || errs[2].Line != 13 || !strings.Contains(errs[2].Msg, "Unterminated") {
		t.Errorf("error lines: %+v", errs)
	}
}

func TestImportDryRunOverwriteAndAutoSecret(t *testing.T) {
	r := newRig(t)
	_, _ = r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "KEEP", Value: sp("original")})
	file := "KEEP=changed\nNEW_ONE=1\nAPI_TOKEN=tok-123\nbad line\n"

	dry, err := r.svc.Import(r.ctx, "u1", "ws1", file, "auto", false, true)
	if err != nil || !dry.DryRun || len(dry.Created) != 2 || len(dry.Skipped) != 1 || len(dry.Errors) != 1 {
		t.Fatalf("dry run: %v %+v", err, dry)
	}
	if env, _ := r.svc.Environment(r.ctx, "ws1"); env["NEW_ONE"] != "" {
		t.Fatal("dry run wrote data")
	}

	res, err := r.svc.Import(r.ctx, "u1", "ws1", file, "auto", false, false)
	if err != nil || len(res.Secrets) != 1 || res.Secrets[0] != "API_TOKEN" {
		t.Fatalf("import: %v %+v", err, res)
	}
	env, _ := r.svc.Environment(r.ctx, "ws1")
	if env["KEEP"] != "original" || env["NEW_ONE"] != "1" || env["API_TOKEN"] != "tok-123" {
		t.Fatalf("env after import: %v", env)
	}
	l, _ := r.svc.List(r.ctx, "u1", "ws1")
	for _, v := range l.Variables {
		if v.Name == "API_TOKEN" && (!v.Secret || v.Value != "") {
			t.Fatalf("auto-secret failed: %+v", v)
		}
	}

	if res, _ = r.svc.Import(r.ctx, "u1", "ws1", "KEEP=changed\n", "none", true, false); len(res.Updated) != 1 {
		t.Fatalf("overwrite: %+v", res)
	}
	if env, _ = r.svc.Environment(r.ctx, "ws1"); env["KEEP"] != "changed" {
		t.Fatal("overwrite not applied")
	}
	// One summary audit event per import, never values.
	evs, _ := r.st.ListAudit(r.ctx, "ws1", 50)
	imports := 0
	for _, e := range evs {
		if e.Action == "variable.imported" {
			imports++
		}
		for _, v := range e.Detail {
			if s, _ := v.(string); strings.Contains(s, "tok-123") {
				t.Fatal("audit leaked a value")
			}
		}
	}
	if imports != 2 {
		t.Fatalf("import audits = %d", imports)
	}
	if _, err := r.svc.Import(r.ctx, "u1", "ws1", "\n# nothing\n", "auto", false, false); err == nil {
		t.Fatal("empty import accepted")
	}
}

func TestExportIsNamesOnly(t *testing.T) {
	r := newRig(t)
	_, _ = r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "STRIPE_KEY", Secret: true, Value: sp("sk_live_VALUE"), Description: "payments"})
	_, _ = r.svc.Set(r.ctx, "u1", "ws1", envvars.SetInput{Name: "LOG_LEVEL", Value: sp("plain-value-123")})
	out, err := r.svc.Export(r.ctx, "u1", "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sk_live_VALUE") || strings.Contains(out, "plain-value-123") || strings.Contains(out, "mongodb://generated") {
		t.Fatalf("export leaked values:\n%s", out)
	}
	for _, want := range []string{"STRIPE_KEY=\n", "LOG_LEVEL=\n", "# secret", "# payments", "PROD_ORDERS_DB_URI"} {
		if !strings.Contains(out, want) {
			t.Fatalf("export missing %q:\n%s", want, out)
		}
	}
}
