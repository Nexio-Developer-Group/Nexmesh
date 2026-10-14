// Package connenv builds the environment variables through which a connected
// service is exposed to workspace projects (PRD 11.6, 20.3.6): predictable
// names derived from the connection name, set only on processes started
// through the workspace agent. Secret values are produced here and never
// shown in the UI or written to disk.
package connenv

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

// ValidName reports whether name can be a connection name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Prefix converts a connection name into an environment variable prefix:
// "prod-orders-db" becomes "PROD_ORDERS_DB".
func Prefix(connName string) string {
	return strings.ToUpper(strings.ReplaceAll(connName, "-", "_"))
}

// Names lists the variables a connection exposes, without values. The UI
// shows these; the naming is provisional (PRD Section 17).
func Names(componentID, connName string) []string {
	p := Prefix(connName)
	switch componentID {
	case "kafka":
		return []string{p + "_BOOTSTRAP_SERVERS", p + "_USERNAME", p + "_PASSWORD"}
	case "redis":
		return []string{p + "_URI", p + "_HOST", p + "_PORT", p + "_PASSWORD"}
	case "externalapi":
		return []string{p + "_URL", p + "_API_KEY_HEADER", p + "_API_KEY", p + "_TOKEN", p + "_USERNAME", p + "_PASSWORD"}
	}
	return []string{p + "_URI", p + "_HOST", p + "_PORT", p + "_DATABASE", p + "_USERNAME", p + "_PASSWORD"}
}

func s(m map[string]any, k string) string {
	if v, ok := m[k]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func truthy(m map[string]any, k string) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func port(m map[string]any, def int) string {
	switch v := m["port"].(type) {
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.Itoa(int(v))
	case string:
		if v != "" {
			return v
		}
	}
	if def > 0 {
		return strconv.Itoa(def)
	}
	return ""
}

func userinfo(user, pass string) *url.Userinfo {
	switch {
	case user == "" && pass == "":
		return nil
	case pass == "":
		return url.User(user)
	}
	return url.UserPassword(user, pass)
}

// URI builds the standard connection string for a component, or "" when the
// technology has none (Kafka). It is the inverse of connstr.Parse.
func URI(componentID string, f map[string]any) string {
	host, user, pass := s(f, "host"), s(f, "username"), s(f, "password")
	hp := host
	if strings.Contains(host, ":") {
		hp = "[" + host + "]"
	}
	switch componentID {
	case "mongodb":
		u := url.URL{Scheme: "mongodb", User: userinfo(user, pass), Host: hp, Path: "/" + s(f, "database")}
		if truthy(f, "srv") {
			u.Scheme = "mongodb+srv"
		} else if p := port(f, 27017); p != "" {
			u.Host = net.JoinHostPort(host, p)
		}
		q := url.Values{}
		if v := s(f, "authSource"); v != "" {
			q.Set("authSource", v)
		}
		if v := s(f, "replicaSet"); v != "" {
			q.Set("replicaSet", v)
		}
		if truthy(f, "tls") && !truthy(f, "srv") {
			q.Set("tls", "true")
		}
		u.RawQuery = q.Encode()
		return u.String()
	case "mysql":
		u := url.URL{Scheme: "mysql", User: userinfo(user, pass), Host: net.JoinHostPort(host, port(f, 3306)), Path: "/" + s(f, "database")}
		if v := s(f, "sslMode"); v != "" && v != "disabled" {
			u.RawQuery = url.Values{"ssl-mode": {v}}.Encode()
		}
		return u.String()
	case "redis":
		scheme := "redis"
		if truthy(f, "tls") {
			scheme = "rediss"
		}
		u := url.URL{Scheme: scheme, User: userinfo(user, pass), Host: net.JoinHostPort(host, port(f, 6379))}
		if db := s(f, "db"); db != "" && db != "0" {
			u.Path = "/" + db
		}
		return u.String()
	case "rabbitmq":
		scheme := "amqp"
		if truthy(f, "tls") {
			scheme = "amqps"
		}
		vhost := s(f, "vhost")
		if vhost == "" {
			vhost = "/"
		}
		u := url.URL{Scheme: scheme, User: userinfo(user, pass), Host: net.JoinHostPort(host, port(f, 5672))}
		u.Path, u.RawPath = "/"+vhost, "/"+url.PathEscape(vhost)
		return u.String()
	}
	return ""
}

// Build returns the variables (including secret values) for one connection.
// fields holds non-secret settings merged with the secret values.
func Build(componentID, connName string, fields map[string]any) map[string]string {
	p := Prefix(connName)
	out := map[string]string{}
	set := func(k, v string) {
		if v != "" {
			out[p+"_"+k] = v
		}
	}
	if componentID == "externalapi" {
		set("URL", strings.TrimRight(s(fields, "baseUrl"), "/"))
		switch s(fields, "authType") {
		case "apiKey":
			if h := s(fields, "apiKeyHeader"); h != "" {
				set("API_KEY_HEADER", h)
			} else {
				set("API_KEY_HEADER", "X-API-Key")
			}
			set("API_KEY", s(fields, "apiKey"))
		case "bearer":
			set("TOKEN", s(fields, "token"))
		case "basic":
			set("USERNAME", s(fields, "username"))
			set("PASSWORD", s(fields, "password"))
		}
		return out
	}
	if componentID == "kafka" {
		set("BOOTSTRAP_SERVERS", s(fields, "bootstrapServers"))
		set("USERNAME", s(fields, "username"))
		set("PASSWORD", s(fields, "password"))
		return out
	}
	set("URI", URI(componentID, fields))
	set("HOST", s(fields, "host"))
	set("PORT", port(fields, 0))
	if componentID != "redis" {
		set("DATABASE", s(fields, "database"))
		set("USERNAME", s(fields, "username"))
	}
	set("PASSWORD", s(fields, "password"))
	return out
}
