package catalog

import (
	"testing"
)

func TestShippedCatalogLoads(t *testing.T) {
	cat, err := Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	// 18 technologies from PRD Section 8 plus the External API card (F16).
	if len(cat.All()) != 19 {
		t.Fatalf("want 19 entries, got %d", len(cat.All()))
	}
	// Mode matrix from PRD 11.2. The External API card is connect-only.
	connectable := map[string]bool{"mongodb": true, "mysql": true, "redis": true, "kafka": true, "rabbitmq": true, "externalapi": true}
	for _, c := range cat.All() {
		if got := c.SupportsMode(ModeConnect); got != connectable[c.ID] {
			t.Errorf("%s connect support = %v, want %v", c.ID, got, connectable[c.ID])
		}
		if c.ID != "externalapi" && !c.SupportsMode(ModeInstall) {
			t.Errorf("%s must be installable", c.ID)
		}
	}
	if ext, _ := cat.Get("externalapi"); ext.SupportsMode(ModeInstall) || len(ext.SecretFields()) != 3 {
		t.Errorf("external API: connect only, with three credential fields (api key, token, password): %v", ext.SecretFields())
	}
	m, _ := cat.Get("mongodb")
	if len(m.SecretFields()) != 1 || m.SecretFields()[0] != "password" {
		t.Errorf("mongodb secret fields: %v", m.SecretFields())
	}
	for _, id := range []string{"python", "nodejs", "go"} {
		c, ok := cat.Get(id)
		if !ok || c.Kind != "runtime" {
			t.Errorf("%s should be a runtime", id)
		}
	}
}

func TestRulesRejectBadManifests(t *testing.T) {
	bad := []Component{
		{ID: "x", Name: "X", Versions: Versions{Default: "2", Supported: []string{"1"}}, SupportedModes: []string{"install"}},
		{ID: "x", Name: "X", Kind: "runtime", Versions: Versions{Default: "1", Supported: []string{"1"}}, SupportedModes: []string{"install", "connect"}, Install: &Install{Strategy: "mise", Tool: "x"}},
		{ID: "x", Name: "X", Kind: "service", Versions: Versions{Default: "1", Supported: []string{"1"}}, SupportedModes: []string{"install"}, Install: &Install{Strategy: "image"}},
	}
	for i := range bad {
		if err := validateRules(&bad[i]); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestShippedProvidersLoad(t *testing.T) {
	ps, err := LoadProviders("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range ps {
		ids = append(ids, p.ID)
		if p.Hosted && len(p.Protocols) != 1 {
			t.Errorf("%s: a hosted provider has exactly one protocol", p.ID)
		}
		for _, f := range p.Fields {
			if f.Name == "apiKey" && f.Type != "password" {
				t.Errorf("%s: apiKey must be a password field", p.ID)
			}
		}
	}
	if got := ids; len(got) != 4 || got[0] != "anthropic" || got[3] != "custom" {
		t.Fatalf("%v", got)
	}
}

func TestShippedTemplatesAreValid(t *testing.T) {
	cat, err := Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := LoadTemplates("../../../catalog", cat)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) < 6 {
		t.Fatalf("expected the curated set, got %d", len(ts))
	}
	for _, tpl := range ts {
		// Templates never run scripts or carry secrets: only the declared fields exist.
		seenPos := map[[2]float64]bool{}
		for _, c := range tpl.Components {
			p := [2]float64{c.X, c.Y}
			if seenPos[p] {
				t.Errorf("%s: two cards at %v would overlap", tpl.ID, p)
			}
			seenPos[p] = true
		}
	}
}

func TestTemplateValidationRejectsBadReferences(t *testing.T) {
	cat, _ := Load("../../../catalog")
	for name, comps := range map[string][]TemplateComponent{
		"unknown":     {{ID: "cobol"}},
		"bad mode":    {{ID: "react", SuggestedMode: "connect"}},
		"bad version": {{ID: "redis", Version: "0.0.1"}},
	} {
		if err := cat.ValidateTemplateComponents(comps); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := cat.ValidateTemplateComponents([]TemplateComponent{{ID: "redis", SuggestedMode: "connect", Version: "7.4.1"}}); err != nil {
		t.Fatal(err)
	}
}
