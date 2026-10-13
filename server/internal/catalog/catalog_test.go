package catalog

import (
	"testing"
)

func TestShippedCatalogLoads(t *testing.T) {
	cat, err := Load("../../../catalog")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.All()) != 18 {
		t.Fatalf("want 18 entries, got %d", len(cat.All()))
	}
	// Mode matrix from PRD 11.2.
	connectable := map[string]bool{"mongodb": true, "mysql": true, "redis": true, "kafka": true, "rabbitmq": true}
	for _, c := range cat.All() {
		if got := c.SupportsMode(ModeConnect); got != connectable[c.ID] {
			t.Errorf("%s connect support = %v, want %v", c.ID, got, connectable[c.ID])
		}
		if !c.SupportsMode(ModeInstall) {
			t.Errorf("%s must be installable", c.ID)
		}
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
