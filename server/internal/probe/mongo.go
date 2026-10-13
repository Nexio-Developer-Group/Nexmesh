package probe

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type mongoSession struct {
	c       *mongo.Client
	p       Params
	host    string
	latency time.Duration
}

type guardedDialer struct{ p Params }

func (d guardedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.p.Guard.DialContext(ctx, network, address)
}

func openMongo(t *Target, p Params) (session, *StageError) {
	opts := options.Client().
		SetHosts([]string{net.JoinHostPort(t.Host, strconv.Itoa(t.Port))}).
		SetDialer(guardedDialer{p}).
		SetServerSelectionTimeout(8 * time.Second).
		SetConnectTimeout(6 * time.Second).
		SetAppName("nexmesh-test-connection")
	if t.TLS != nil {
		opts.SetTLSConfig(t.TLS)
	}
	if rs := str(p.Fields, "replicaSet"); rs != "" {
		opts.SetReplicaSet(rs)
	} else {
		opts.SetDirect(true)
	}
	if user := str(p.Fields, "username"); user != "" {
		src := str(p.Fields, "authSource")
		if src == "" {
			src = "admin"
		}
		opts.SetAuth(options.Credential{Username: user, Password: str(p.Fields, "password"), AuthSource: src})
	}
	c, err := mongo.Connect(opts)
	if err != nil {
		return nil, stageErr("The connection settings are not valid.")
	}
	return &mongoSession{c: c, p: p, host: t.Host}, nil
}

func (s *mongoSession) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.c.Disconnect(ctx)
}

func (s *mongoSession) Authenticate(ctx context.Context) *StageError {
	start := time.Now()
	err := s.c.Ping(ctx, nil)
	s.latency = time.Since(start)
	if err == nil {
		return nil
	}
	var ce mongo.CommandError
	m := strings.ToLower(err.Error())
	switch {
	case errors.As(err, &ce) && (ce.Code == 18 || ce.Code == 11):
		return stageErr("Host reachable but authentication failed. Check the username, password and auth source.")
	case strings.Contains(m, "authentication failed"), strings.Contains(m, "auth error"), strings.Contains(m, "scram"):
		return stageErr("Host reachable but authentication failed. Check the username, password and auth source.")
	case strings.Contains(m, "certificate"), strings.Contains(m, "x509"), strings.Contains(m, "tls"):
		return classifyTLS(err, s.host)
	case strings.Contains(m, "server selection"), strings.Contains(m, "timeout"):
		return stageErr("The server did not respond to MongoDB commands. Check that this is a MongoDB port and, for replica sets, the replica set name.")
	}
	return stageErr("MongoDB rejected the connection: %s", s.p.Scrub(firstLine(err)))
}

func (s *mongoSession) Authorize(ctx context.Context) *StageError {
	db := str(s.p.Fields, "database")
	if db == "" {
		db = "admin"
	}
	if _, err := s.c.Database(db).ListCollectionNames(ctx, bson.D{}); err != nil {
		var ce mongo.CommandError
		if errors.As(err, &ce) && ce.Code == 13 {
			return stageErr("Login accepted but this account may not read database %q. Grant at least the read role.", db)
		}
		return stageErr("A read-only check failed: %s", s.p.Scrub(firstLine(err)))
	}
	return nil
}

func (s *mongoSession) Details(ctx context.Context) (Details, *StageError) {
	var out struct {
		Version string `bson:"version"`
	}
	if err := s.c.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&out); err != nil || out.Version == "" {
		// buildInfo may need privileges on locked-down accounts; the ping already proved reachability.
		return Details{Latency: s.latency, Info: "(version hidden by permissions)"}, nil
	}
	return Details{Version: out.Version, Latency: s.latency}, nil
}
