// Package connstr parses pasted connection strings into the field names used
// by the catalog's connection forms (PRD 11.3 step 2). The user always reviews
// the parsed values; nothing is connected from a string directly.
//
// Parse errors describe the problem's location, never the input, so a
// password in a malformed string cannot leak into an error message.
package connstr

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const MaxLen = 4096

// Error is a parse failure with a user-safe message.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func fail(format string, a ...any) error { return &Error{Msg: fmt.Sprintf(format, a...)} }

// Parse converts uri into form fields for the catalog component id.
func Parse(componentID, uri string) (map[string]any, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, fail("Paste a connection string first.")
	}
	if len(uri) > MaxLen {
		return nil, fail("That connection string is too long.")
	}
	for _, r := range uri {
		if r < 0x20 || r == 0x7f {
			return nil, fail("The connection string contains control characters.")
		}
	}
	switch componentID {
	case "mongodb":
		return parseMongo(uri)
	case "mysql":
		return parseMySQL(uri)
	case "redis":
		return parseRedis(uri)
	case "rabbitmq":
		return parseAMQP(uri)
	case "kafka":
		return nil, fail("Kafka has no standard connection string. Enter the bootstrap servers as host:port.")
	}
	return nil, fail("This component does not support connection strings.")
}

// split parses scheme://userinfo@host:port/path?query and validates the scheme.
func split(uri string, schemes ...string) (*url.URL, error) {
	// Multi-host strings (replica set seeds) are rejected before url.Parse so
	// the error is specific.
	if i := strings.Index(uri, "://"); i > 0 {
		rest := uri[i+3:]
		auth := rest
		if j := strings.IndexAny(rest, "/?"); j >= 0 {
			auth = rest[:j]
		}
		if at := strings.LastIndex(auth, "@"); at >= 0 {
			auth = auth[at+1:]
		}
		if strings.Contains(auth, ",") {
			return nil, fail("Multiple hosts are not supported yet. Enter a single host.")
		}
	}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fail("That is not a valid connection string.")
	}
	ok := false
	for _, s := range schemes {
		if strings.EqualFold(u.Scheme, s) {
			ok = true
		}
	}
	if !ok {
		return nil, fail("Expected a connection string starting with %s://", strings.Join(schemes, "://, "))
	}
	if u.Hostname() == "" {
		return nil, fail("The connection string has no host.")
	}
	return u, nil
}

func common(u *url.URL, out map[string]any) error {
	out["host"] = u.Hostname()
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fail("The port must be a number between 1 and 65535.")
		}
		out["port"] = n
	}
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			out["username"] = name
		}
		if pw, ok := u.User.Password(); ok && pw != "" {
			out["password"] = pw
		}
	}
	return nil
}

func dbName(u *url.URL) string { return strings.TrimPrefix(u.Path, "/") }

func truthy(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on", "required", "verify-ca", "verify-full", "verify_ca", "verify_identity":
		return true
	}
	return false
}

func parseMongo(uri string) (map[string]any, error) {
	u, err := split(uri, "mongodb", "mongodb+srv")
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := common(u, out); err != nil {
		return nil, err
	}
	srv := strings.EqualFold(u.Scheme, "mongodb+srv")
	out["srv"] = srv
	if srv {
		delete(out, "port") // SRV records supply the port
		out["tls"] = true   // SRV implies TLS unless disabled below
	} else if _, ok := out["port"]; !ok {
		out["port"] = 27017
	}
	if db := dbName(u); db != "" {
		out["database"] = db
	}
	q := u.Query()
	if v := q.Get("authSource"); v != "" {
		out["authSource"] = v
	}
	if v := q.Get("replicaSet"); v != "" {
		out["replicaSet"] = v
	}
	for _, k := range []string{"tls", "ssl"} {
		if v := q.Get(k); v != "" {
			out["tls"] = truthy(v)
		}
	}
	return out, nil
}

func parseMySQL(uri string) (map[string]any, error) {
	u, err := split(uri, "mysql")
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := common(u, out); err != nil {
		return nil, err
	}
	if _, ok := out["port"]; !ok {
		out["port"] = 3306
	}
	if db := dbName(u); db != "" {
		out["database"] = db
	}
	q := u.Query()
	for _, k := range []string{"ssl-mode", "ssl_mode", "sslmode", "sslMode"} {
		if v := q.Get(k); v != "" {
			mode := strings.ToLower(strings.ReplaceAll(v, "_", "-"))
			switch mode {
			case "disabled", "preferred", "required", "verify-ca", "verify-full":
				out["sslMode"] = mode
			case "verify-identity":
				out["sslMode"] = "verify-full"
			default:
				return nil, fail("Unknown ssl mode. Use disabled, preferred, required, verify-ca or verify-full.")
			}
		}
	}
	if v := q.Get("timeout"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "s")); err == nil && n > 0 {
			out["timeoutSeconds"] = n
		}
	}
	return out, nil
}

func parseRedis(uri string) (map[string]any, error) {
	u, err := split(uri, "redis", "rediss")
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := common(u, out); err != nil {
		return nil, err
	}
	// redis://:password@host has an empty username; redis://token@host is a bare password (legacy form is not supported).
	if _, ok := out["port"]; !ok {
		out["port"] = 6379
	}
	out["tls"] = strings.EqualFold(u.Scheme, "rediss")
	if db := dbName(u); db != "" {
		n, err := strconv.Atoi(db)
		if err != nil || n < 0 || n > 255 {
			return nil, fail("The database index must be a number between 0 and 255.")
		}
		out["db"] = n
	}
	return out, nil
}

func parseAMQP(uri string) (map[string]any, error) {
	u, err := split(uri, "amqp", "amqps")
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := common(u, out); err != nil {
		return nil, err
	}
	tls := strings.EqualFold(u.Scheme, "amqps")
	out["tls"] = tls
	if _, ok := out["port"]; !ok {
		if tls {
			out["port"] = 5671
		} else {
			out["port"] = 5672
		}
	}
	// The vhost is the first path segment, percent-decoded; "/" is the default.
	vhost := "/"
	if p := strings.TrimPrefix(u.EscapedPath(), "/"); p != "" {
		d, err := url.PathUnescape(p)
		if err != nil {
			return nil, fail("The virtual host is not valid.")
		}
		vhost = d
	}
	out["vhost"] = vhost
	return out, nil
}
