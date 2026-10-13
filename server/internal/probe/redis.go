package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type redisSession struct {
	host    string
	c       *redis.Client
	p       Params
	latency time.Duration
}

func openRedis(t *Target, p Params) (session, *StageError) {
	opt := &redis.Options{
		Addr:            net.JoinHostPort(t.Host, strconv.Itoa(t.Port)),
		Username:        str(p.Fields, "username"),
		Password:        str(p.Fields, "password"),
		DB:              intField(p.Fields, "db", 0),
		DialTimeout:     6 * time.Second,
		ReadTimeout:     6 * time.Second,
		MaxRetries:      -1,
		PoolSize:        1,
		DisableIdentity: true, // do not send CLIENT SETINFO: probes must not change server state
		// A custom Dialer replaces go-redis's own TLS wrapping, so TLS is applied here.
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := p.Guard.DialContext(ctx, network, addr)
			if err != nil || t.TLS == nil {
				return c, err
			}
			tc := tls.Client(c, t.TLS.Clone())
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = c.Close()
				return nil, err
			}
			return tc, nil
		},
	}
	return &redisSession{c: redis.NewClient(opt), p: p, host: t.Host}, nil
}

func (s *redisSession) Close() { _ = s.c.Close() }

func (s *redisSession) Authenticate(ctx context.Context) *StageError {
	start := time.Now()
	err := s.c.Ping(ctx).Err()
	s.latency = time.Since(start)
	if err == nil {
		return nil
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "wrongpass"), strings.Contains(m, "invalid username-password"), strings.Contains(m, "noauth"),
		strings.Contains(m, "err auth"), strings.Contains(m, "authentication required"), strings.Contains(m, "invalid password"):
		return stageErr("Host reachable but authentication failed. Check the username and password.")
	case strings.Contains(m, "certificate"), strings.Contains(m, "tls"):
		return classifyTLS(err, s.host)
	}
	return stageErr("Redis rejected the connection: %s", s.p.Scrub(firstLine(err)))
}

func (s *redisSession) Authorize(ctx context.Context) *StageError {
	if err := s.c.Info(ctx, "server").Err(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "noperm") {
			return stageErr("Login accepted but this account may not run INFO. Grant read access (for example +info).")
		}
		return stageErr("A read-only check failed: %s", s.p.Scrub(firstLine(err)))
	}
	return nil
}

func (s *redisSession) Details(ctx context.Context) (Details, *StageError) {
	info, err := s.c.Info(ctx, "server").Result()
	if err != nil {
		return Details{}, stageErr("Could not read server info: %s", s.p.Scrub(firstLine(err)))
	}
	d := Details{Latency: s.latency}
	for _, line := range strings.Split(info, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "redis_version:"); ok {
			d.Version = v
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "redis_mode:"); ok {
			d.Info = "(" + v + ")"
		}
	}
	return d, nil
}

func firstLine(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timed out"
	}
	m := err.Error()
	if i := strings.IndexByte(m, '\n'); i > 0 {
		m = m[:i]
	}
	if len(m) > 160 {
		m = m[:160] + "…"
	}
	return m
}
