package probe

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type mysqlSession struct {
	db      *sql.DB
	p       Params
	host    string
	latency time.Duration
}

func openMySQL(t *Target, p Params) (session, *StageError) {
	cfg := mysql.NewConfig()
	cfg.User = str(p.Fields, "username")
	cfg.Passwd = str(p.Fields, "password")
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	cfg.DBName = str(p.Fields, "database")
	timeout := time.Duration(intField(p.Fields, "timeoutSeconds", 10)) * time.Second
	cfg.Timeout = timeout
	cfg.ReadTimeout = 8 * time.Second
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return p.Guard.DialContext(ctx, network, addr)
	}
	switch str(p.Fields, "sslMode") {
	case "disabled", "":
		cfg.TLS = nil
	case "preferred":
		cfg.TLS = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} //nolint:gosec // MySQL "preferred" semantics
		cfg.AllowFallbackToPlaintext = true
	case "required":
		cfg.TLS = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} //nolint:gosec // MySQL "required" = encrypted, unverified
	default: // verify-ca, verify-full
		cfg.TLS = t.TLS
		if str(p.Fields, "sslMode") == "verify-ca" && cfg.TLS != nil {
			cfg.TLS = verifyCAOnly(cfg.TLS)
		}
	}
	conn, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, stageErr("The connection settings are not valid.")
	}
	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(30 * time.Second)
	return &mysqlSession{db: db, p: p, host: t.Host}, nil
}

// verifyCAOnly validates the chain but not the host name (MySQL verify-ca).
func verifyCAOnly(base *tls.Config) *tls.Config {
	c := base.Clone()
	c.InsecureSkipVerify = true //nolint:gosec // chain is verified manually below
	c.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509Cert) error { return verifyChain(raw, base.RootCAs) }
	return c
}

func (s *mysqlSession) Close() { _ = s.db.Close() }

func (s *mysqlSession) Authenticate(ctx context.Context) *StageError {
	start := time.Now()
	err := s.db.PingContext(ctx)
	s.latency = time.Since(start)
	if err == nil {
		return nil
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1045:
			return stageErr("Host reachable but authentication failed. Check the username and password.")
		case 1044, 1049:
			return stageErr("Login accepted but the database %q is not available to this account.", str(s.p.Fields, "database"))
		}
		return stageErr("MySQL rejected the connection (error %d).", me.Number)
	}
	if strings.Contains(err.Error(), "TLS") || strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "x509") {
		return classifyTLS(err, s.host)
	}
	if strings.Contains(err.Error(), "SSL") {
		return stageErr("The server does not support TLS, but this connection requires it. Change the SSL mode or enable TLS on the server.")
	}
	return stageErr("Could not complete the MySQL handshake: %s", s.p.Scrub(firstLine(err)))
}

func (s *mysqlSession) Authorize(ctx context.Context) *StageError {
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && (me.Number == 1044 || me.Number == 1142 || me.Number == 1045) {
			return stageErr("Login accepted but this account may not run queries on %q.", str(s.p.Fields, "database"))
		}
		return stageErr("A read-only query failed: %s", s.p.Scrub(firstLine(err)))
	}
	return nil
}

func (s *mysqlSession) Details(ctx context.Context) (Details, *StageError) {
	var v string
	if err := s.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v); err != nil {
		return Details{}, stageErr("Could not read the server version.")
	}
	return Details{Version: strings.SplitN(v, "-", 2)[0], Info: infoSuffix(v), Latency: s.latency}, nil
}

func infoSuffix(v string) string {
	if i := strings.Index(v, "-"); i > 0 {
		return "(" + strings.ToLower(v[i+1:]) + ")"
	}
	return ""
}
