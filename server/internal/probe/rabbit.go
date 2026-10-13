package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type rabbitSession struct {
	raw     net.Conn
	p       Params
	t       *Target
	conn    *amqp.Connection
	latency time.Duration
}

func openRabbit(t *Target, p Params) (session, *StageError) { return &rabbitSession{p: p, t: t}, nil }

func (s *rabbitSession) Close() {
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func (s *rabbitSession) Authenticate(ctx context.Context) *StageError {
	scheme := "amqp"
	if s.t.UseTLS {
		scheme = "amqps"
	}
	vhost := str(s.p.Fields, "vhost")
	if vhost == "" {
		vhost = "/"
	}
	cfg := amqp.Config{
		Vhost:           vhost,
		SASL:            []amqp.Authentication{&amqp.PlainAuth{Username: str(s.p.Fields, "username"), Password: str(s.p.Fields, "password")}},
		TLSClientConfig: s.t.TLS,
		Locale:          "en_US",
		Heartbeat:       0,
		Dial: func(network, addr string) (net.Conn, error) {
			dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			c, err := s.p.Guard.DialContext(dctx, network, addr)
			if err == nil {
				// amqp091 sets no handshake deadline for custom dialers: a server
				// that never answers the AMQP header must not hang the probe.
				_ = c.SetDeadline(time.Now().Add(8 * time.Second))
				s.raw = c
			}
			return c, err
		},
	}
	start := time.Now()
	conn, err := amqp.DialConfig(fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(s.t.Host, strconv.Itoa(s.t.Port))), cfg)
	s.latency = time.Since(start)
	if err != nil {
		var ae *amqp.Error
		m := strings.ToLower(err.Error())
		switch {
		case strings.Contains(m, "vhost"), errors.As(err, &ae) && ae.Code == 530, strings.Contains(m, "not_allowed"):
			return stageErr("Login accepted but virtual host %q is not available to this account.", vhost)
		case errors.As(err, &ae) && ae.Code == 403, strings.Contains(m, "access_refused"), strings.Contains(m, "(403)"):
			return stageErr("Host reachable but authentication failed. Check the username and password.")
		case strings.Contains(m, "certificate"), strings.Contains(m, "x509"), strings.Contains(m, "tls"):
			return classifyTLS(err, s.t.Host)
		case strings.Contains(m, "eof"), strings.Contains(m, "malformed"), strings.Contains(m, "unexpected"), strings.Contains(m, "timeout"), strings.Contains(m, "protocol"):
			return stageErr("The server did not speak AMQP on this port. Check the port and the TLS setting.")
		}
		return stageErr("RabbitMQ rejected the connection: %s", s.p.Scrub(firstLine(err)))
	}
	s.conn = conn
	if s.raw != nil {
		_ = s.raw.SetDeadline(time.Time{})
	}
	return nil
}

func (s *rabbitSession) Authorize(ctx context.Context) *StageError {
	ch, err := s.conn.Channel()
	if err != nil {
		return stageErr("Login accepted but a channel could not be opened: %s", s.p.Scrub(firstLine(err)))
	}
	defer ch.Close()
	// Passive declare of a built-in exchange: read-only, creates nothing.
	if err := ch.ExchangeDeclarePassive("amq.direct", "direct", true, false, false, false, nil); err != nil {
		var ae *amqp.Error
		if errors.As(err, &ae) && ae.Code == 403 {
			return stageErr("Login accepted but this account has no permissions in virtual host %q.", str(s.p.Fields, "vhost"))
		}
		return stageErr("A read-only check failed: %s", s.p.Scrub(firstLine(err)))
	}
	return nil
}

func (s *rabbitSession) Details(ctx context.Context) (Details, *StageError) {
	d := Details{Latency: s.latency}
	if v, ok := s.conn.Properties["version"].(string); ok {
		d.Version = v
	}
	if prod, ok := s.conn.Properties["product"].(string); ok && prod != "RabbitMQ" {
		d.Info = "(" + prod + ")"
	}
	return d, nil
}
