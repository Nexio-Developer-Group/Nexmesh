// Package connimport reads connection details from a .env file, a pasted
// connection string or a docker-compose file (PRD F14) and proposes values for
// the Connect form. Nothing is stored or connected here: the user reviews the
// values, and Test Connection is still required (PRD 11.5). Parsed values,
// which may include passwords, go back to the caller only.
package connimport

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nexio-developer-group/nexmesh/server/internal/catalog"
	"github.com/nexio-developer-group/nexmesh/server/internal/connstr"
	"github.com/nexio-developer-group/nexmesh/server/internal/envvars"
)

const MaxInput = 256 << 10

// Candidate is one connection found in the input.
type Candidate struct {
	Source    string         `json:"source"`    // variable name, compose service, or "connection string"
	CatalogID string         `json:"catalogId"` // detected component type
	Name      string         `json:"name"`      // suggested connection name
	Fields    map[string]any `json:"fields"`
	Secrets   []string       `json:"secrets"` // field names whose values are credentials
	Missing   []string       `json:"missing"` // required fields the input did not provide
	Notes     []string       `json:"notes,omitempty"`
}

type Skipped struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

type Result struct {
	Format     string      `json:"format"` // connection-string | env | compose
	Candidates []Candidate `json:"candidates"`
	Skipped    []Skipped   `json:"skipped"`
}

// Error is a user-presentable problem with the input.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

const supported = "MongoDB, MySQL or MariaDB, Redis, RabbitMQ and Kafka"

type Importer struct{ cat *catalog.Catalog }

func New(cat *catalog.Catalog) *Importer { return &Importer{cat: cat} }

// Import detects the format of input and returns the connections it describes.
// If catalogID is set, only that component type is returned.
func (im *Importer) Import(input, catalogID string) (*Result, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, &Error{"Paste a connection string, a .env file or a docker-compose file."}
	}
	if len(input) > MaxInput {
		return nil, &Error{"That is too large to import (limit 256 KB)."}
	}
	var res *Result
	switch {
	case isCompose(input):
		res = im.fromCompose(input)
	case !strings.ContainsAny(input, "\n") && strings.Contains(input, "://") && !strings.Contains(input, "="):
		res = im.fromString(input)
	default:
		res = im.fromEnv(input)
	}
	if catalogID != "" {
		kept := res.Candidates[:0]
		for _, c := range res.Candidates {
			if c.CatalogID == catalogID {
				kept = append(kept, c)
			}
		}
		res.Candidates = kept
	}
	if len(res.Candidates) == 0 {
		hint := ""
		if catalogID != "" {
			if def, ok := im.cat.Get(catalogID); ok {
				hint = " for " + def.Name
			}
		}
		return nil, &Error{fmt.Sprintf("No supported connection%s was found. Supported: %s.", hint, supported)}
	}
	for i := range res.Candidates {
		im.finish(&res.Candidates[i])
	}
	sort.SliceStable(res.Candidates, func(i, j int) bool { return res.Candidates[i].Source < res.Candidates[j].Source })
	return res, nil
}

// finish fills defaults, secret names and missing required fields from the catalog.
func (im *Importer) finish(c *Candidate) {
	def, ok := im.cat.Get(c.CatalogID)
	if !ok || def.Connection == nil {
		return
	}
	c.Secrets = []string{}
	c.Missing = []string{}
	for _, f := range def.Connection.Fields {
		v, have := c.Fields[f.Name]
		if f.Type == "password" && have && fmt.Sprint(v) != "" {
			c.Secrets = append(c.Secrets, f.Name)
		}
		if f.Required && (!have || fmt.Sprint(v) == "" || v == nil) && f.Default == nil {
			c.Missing = append(c.Missing, f.Name)
		}
	}
	c.Name = connName(c.Name, c.CatalogID)
}

var nameClean = regexp.MustCompile(`[^a-z0-9]+`)

func connName(src, catalogID string) string {
	n := strings.Trim(nameClean.ReplaceAllString(strings.ToLower(src), "-"), "-")
	for _, suffix := range []string{"-url", "-uri", "-dsn", "-connection-string"} {
		n = strings.TrimSuffix(n, suffix)
	}
	if n == "" || n == "connection-string" {
		n = catalogID
	}
	if len(n) > 40 {
		n = n[:40]
	}
	return n
}

// --- connection strings and URLs ---

func schemeComponent(uri string) string {
	i := strings.Index(uri, "://")
	if i <= 0 {
		return ""
	}
	switch strings.ToLower(uri[:i]) {
	case "mongodb", "mongodb+srv":
		return "mongodb"
	case "mysql", "mariadb":
		return "mysql"
	case "redis", "rediss":
		return "redis"
	case "amqp", "amqps":
		return "rabbitmq"
	}
	return ""
}

func (im *Importer) fromString(s string) *Result {
	res := &Result{Format: "connection-string", Candidates: []Candidate{}, Skipped: []Skipped{}}
	id := schemeComponent(s)
	if id == "" {
		res.Skipped = append(res.Skipped, Skipped{"connection string", "The scheme is not one of mongodb, mysql, redis or amqp."})
		return res
	}
	norm := s
	if strings.HasPrefix(strings.ToLower(s), "mariadb://") {
		norm = "mysql://" + s[len("mariadb://"):]
	}
	fields, err := connstr.Parse(id, norm)
	if err != nil {
		res.Skipped = append(res.Skipped, Skipped{"connection string", err.Error()})
		return res
	}
	res.Candidates = append(res.Candidates, Candidate{Source: "connection string", CatalogID: id, Name: id, Fields: fields})
	return res
}

// --- .env ---

type envGroup struct {
	catalog  string
	prefixes []string
	// key suffix (after the prefix) -> field name
	keys map[string]string
}

var envGroups = []envGroup{
	{"mongodb", []string{"MONGODB_", "MONGO_"}, map[string]string{"HOST": "host", "HOSTNAME": "host", "PORT": "port", "DB": "database", "DATABASE": "database", "NAME": "database", "USER": "username", "USERNAME": "username", "PASSWORD": "password", "PASS": "password"}},
	{"mysql", []string{"MYSQL_", "MARIADB_"}, map[string]string{"HOST": "host", "HOSTNAME": "host", "PORT": "port", "DB": "database", "DATABASE": "database", "NAME": "database", "USER": "username", "USERNAME": "username", "PASSWORD": "password", "PASS": "password"}},
	{"redis", []string{"REDIS_"}, map[string]string{"HOST": "host", "HOSTNAME": "host", "PORT": "port", "USER": "username", "USERNAME": "username", "PASSWORD": "password", "PASS": "password", "DB": "db", "TLS": "tls"}},
	{"rabbitmq", []string{"RABBITMQ_", "AMQP_"}, map[string]string{"HOST": "host", "HOSTNAME": "host", "PORT": "port", "USER": "username", "USERNAME": "username", "PASSWORD": "password", "PASS": "password", "VHOST": "vhost"}},
}

func (im *Importer) fromEnv(input string) *Result {
	vars, errs := envvars.ParseDotenv(input)
	res := &Result{Format: "env", Candidates: []Candidate{}, Skipped: []Skipped{}}
	for _, e := range errs {
		res.Skipped = append(res.Skipped, Skipped{fmt.Sprintf("line %d", e.Line), e.Msg})
	}
	used := map[string]bool{}
	// 1. Variables whose value is a connection URL.
	for _, v := range vars {
		id := schemeComponent(v.Value)
		if id == "" {
			continue
		}
		used[v.Name] = true
		norm := v.Value
		if strings.HasPrefix(strings.ToLower(norm), "mariadb://") {
			norm = "mysql://" + norm[len("mariadb://"):]
		}
		fields, err := connstr.Parse(id, norm)
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{v.Name, err.Error()})
			continue
		}
		res.Candidates = append(res.Candidates, Candidate{Source: v.Name, CatalogID: id, Name: v.Name, Fields: fields})
	}
	// 2. Groups of discrete variables: MONGO_HOST, MONGO_USER, ...
	for _, g := range envGroups {
		fields := map[string]any{}
		var srcs []string
		for _, v := range vars {
			if used[v.Name] {
				continue
			}
			for _, p := range g.prefixes {
				if rest, ok := strings.CutPrefix(v.Name, p); ok {
					if f, ok := g.keys[rest]; ok && v.Value != "" {
						if _, dup := fields[f]; !dup {
							fields[f] = coerce(f, v.Value)
							srcs = append(srcs, v.Name)
						}
					}
				}
			}
		}
		if _, ok := fields["host"]; ok {
			res.Candidates = append(res.Candidates, Candidate{Source: strings.TrimSuffix(strings.Split(srcs[0], "_")[0], "_"), CatalogID: g.catalog, Name: g.catalog, Fields: fields,
				Notes: []string{"Built from " + strings.Join(srcs, ", ")}})
		}
	}
	// 3. Kafka brokers.
	for _, v := range vars {
		switch v.Name {
		case "KAFKA_BROKERS", "KAFKA_BOOTSTRAP_SERVERS", "KAFKA_BOOTSTRAP_SERVER", "KAFKA_BROKER", "KAFKA_URL":
			if v.Value == "" {
				continue
			}
			f := map[string]any{"bootstrapServers": strings.TrimPrefix(strings.TrimPrefix(v.Value, "PLAINTEXT://"), "kafka://")}
			for _, v2 := range vars {
				switch v2.Name {
				case "KAFKA_SASL_USERNAME", "KAFKA_USERNAME":
					f["username"] = v2.Value
				case "KAFKA_SASL_PASSWORD", "KAFKA_PASSWORD":
					f["password"] = v2.Value
				}
			}
			res.Candidates = append(res.Candidates, Candidate{Source: v.Name, CatalogID: "kafka", Name: "kafka", Fields: f})
		}
	}
	return res
}

func coerce(field, v string) any {
	switch field {
	case "port", "db":
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	case "tls":
		return strings.EqualFold(v, "true") || v == "1"
	}
	return v
}

// --- docker-compose ---

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image       string `yaml:"image"`
	Environment any    `yaml:"environment"`
	Ports       []any  `yaml:"ports"`
	Command     any    `yaml:"command"`
}

func isCompose(s string) bool {
	if !strings.Contains(s, "services:") {
		return false
	}
	var f composeFile
	return yaml.Unmarshal([]byte(s), &f) == nil && len(f.Services) > 0
}

var composeVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?-([^}]*))?\}`)

// expand resolves ${VAR:-default} to its default; a variable with no default
// stays unknown (empty), because its value lives outside the file.
func expand(s string) string { return composeVar.ReplaceAllString(s, "$2") }

func composeEnv(v any) map[string]string {
	out := map[string]string{}
	switch e := v.(type) {
	case map[string]any:
		for k, val := range e {
			out[k] = expand(fmt.Sprint(val))
		}
	case []any:
		for _, item := range e {
			k, val, ok := strings.Cut(fmt.Sprint(item), "=")
			if ok {
				out[k] = expand(val)
			}
		}
	}
	return out
}

func imageComponent(image string) string {
	img := strings.ToLower(image)
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	img = strings.SplitN(img, ":", 2)[0]
	switch {
	case strings.Contains(img, "mongo"):
		return "mongodb"
	case strings.Contains(img, "mysql") || strings.Contains(img, "mariadb"):
		return "mysql"
	case strings.Contains(img, "redis") || strings.Contains(img, "valkey"):
		return "redis"
	case strings.Contains(img, "rabbitmq"):
		return "rabbitmq"
	case strings.Contains(img, "kafka") || strings.Contains(img, "redpanda"):
		return "kafka"
	}
	return ""
}

// publishedPort returns the host-side port of the first mapping to containerPort ("27018:27017").
func publishedPort(ports []any, containerPort int) (int, bool) {
	for _, p := range ports {
		s := strings.Split(fmt.Sprint(p), "/")[0]
		parts := strings.Split(s, ":")
		if len(parts) >= 2 {
			host, _ := strconv.Atoi(parts[len(parts)-2])
			cont, _ := strconv.Atoi(parts[len(parts)-1])
			if cont == containerPort && host > 0 {
				return host, true
			}
		}
	}
	return 0, false
}

func (im *Importer) fromCompose(input string) *Result {
	res := &Result{Format: "compose", Candidates: []Candidate{}, Skipped: []Skipped{}}
	var f composeFile
	_ = yaml.Unmarshal([]byte(input), &f)
	names := make([]string, 0, len(f.Services))
	for n := range f.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := f.Services[name]
		id := imageComponent(svc.Image)
		if id == "" {
			res.Skipped = append(res.Skipped, Skipped{name, "Not a supported service (image " + orNone(svc.Image) + ")."})
			continue
		}
		env := composeEnv(svc.Environment)
		fields := map[string]any{}
		notes := []string{"The host is not known from a compose file: enter where this service is reachable from the workspace."}
		set := func(k string, v any) {
			if s, ok := v.(string); ok && s == "" {
				return
			}
			fields[k] = v
		}
		switch id {
		case "mongodb":
			set("username", env["MONGO_INITDB_ROOT_USERNAME"])
			set("password", env["MONGO_INITDB_ROOT_PASSWORD"])
			set("database", env["MONGO_INITDB_DATABASE"])
			port := 27017
			if p, ok := publishedPort(svc.Ports, 27017); ok {
				port = p
			}
			fields["port"] = port
		case "mysql":
			user, pw := env["MYSQL_USER"], env["MYSQL_PASSWORD"]
			if user == "" {
				user, pw = "root", firstNonEmpty(env["MYSQL_ROOT_PASSWORD"], env["MARIADB_ROOT_PASSWORD"])
			}
			set("username", user)
			set("password", pw)
			set("database", firstNonEmpty(env["MYSQL_DATABASE"], env["MARIADB_DATABASE"]))
			port := 3306
			if p, ok := publishedPort(svc.Ports, 3306); ok {
				port = p
			}
			fields["port"] = port
		case "redis":
			pw := env["REDIS_PASSWORD"]
			if cmd := commandString(svc.Command); pw == "" && strings.Contains(cmd, "--requirepass") {
				fs := strings.Fields(cmd)
				for i, w := range fs {
					if w == "--requirepass" && i+1 < len(fs) {
						pw = strings.Trim(fs[i+1], `"'`)
					}
				}
			}
			set("password", pw)
			port := 6379
			if p, ok := publishedPort(svc.Ports, 6379); ok {
				port = p
			}
			fields["port"] = port
		case "rabbitmq":
			set("username", env["RABBITMQ_DEFAULT_USER"])
			set("password", env["RABBITMQ_DEFAULT_PASS"])
			set("vhost", env["RABBITMQ_DEFAULT_VHOST"])
			port := 5672
			if p, ok := publishedPort(svc.Ports, 5672); ok {
				port = p
			}
			fields["port"] = port
		case "kafka":
			notes = []string{"Kafka's bootstrap servers (host:port) are not known from a compose file: enter an address reachable from the workspace."}
		}
		res.Candidates = append(res.Candidates, Candidate{Source: name, CatalogID: id, Name: name, Fields: fields, Notes: notes})
	}
	return res
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func commandString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, p := range c {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, " ")
	}
	return ""
}
