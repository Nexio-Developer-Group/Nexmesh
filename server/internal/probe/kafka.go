package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
)

type kafkaSession struct {
	p       Params
	t       *Target
	conn    *kafka.Conn
	latency time.Duration
	brokers int
	topics  int
}

func openKafka(t *Target, p Params) (session, *StageError) { return &kafkaSession{p: p, t: t}, nil }

func (s *kafkaSession) Close() {
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func saslMechanism(p Params) (sasl.Mechanism, *StageError) {
	proto := str(p.Fields, "securityProtocol")
	if proto != "SASL_PLAINTEXT" && proto != "SASL_SSL" {
		return nil, nil
	}
	user, pass := str(p.Fields, "username"), str(p.Fields, "password")
	if user == "" || pass == "" {
		return nil, stageErr("SASL needs a username (or API key) and password (or API secret).")
	}
	switch str(p.Fields, "saslMechanism") {
	case "", "PLAIN":
		return plain.Mechanism{Username: user, Password: pass}, nil
	case "SCRAM-SHA-256":
		m, err := scram.Mechanism(scram.SHA256, user, pass)
		if err != nil {
			return nil, stageErr("The SCRAM credentials are not valid.")
		}
		return m, nil
	case "SCRAM-SHA-512":
		m, err := scram.Mechanism(scram.SHA512, user, pass)
		if err != nil {
			return nil, stageErr("The SCRAM credentials are not valid.")
		}
		return m, nil
	}
	return nil, stageErr("Unsupported SASL mechanism.")
}

func (s *kafkaSession) Authenticate(ctx context.Context) *StageError {
	mech, e := saslMechanism(s.p)
	if e != nil {
		return e
	}
	clientID := str(s.p.Fields, "clientId")
	if clientID == "" {
		clientID = "nexmesh"
	}
	d := &kafka.Dialer{
		Timeout:       6 * time.Second,
		ClientID:      clientID,
		TLS:           s.t.TLS,
		SASLMechanism: mech,
		DialFunc: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return s.p.Guard.DialContext(ctx, network, addr)
		},
	}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(s.t.Host, strconv.Itoa(s.t.Port)))
	s.latency = time.Since(start)
	if err != nil {
		m := strings.ToLower(err.Error())
		switch {
		case strings.Contains(m, "sasl"), strings.Contains(m, "authentication"):
			return stageErr("Host reachable but authentication failed. Check the username and password (or API key and secret).")
		case strings.Contains(m, "certificate"), strings.Contains(m, "x509"), strings.Contains(m, "tls"):
			return classifyTLS(err, s.t.Host)
		case strings.Contains(m, "eof"), strings.Contains(m, "unexpected"), strings.Contains(m, "connection reset"):
			return stageErr("The broker closed the connection. Check the security protocol: it must match the listener's configuration.")
		}
		return stageErr("Kafka rejected the connection: %s", s.p.Scrub(firstLine(err)))
	}
	s.conn = conn
	return nil
}

func (s *kafkaSession) Authorize(ctx context.Context) *StageError {
	_ = s.conn.SetDeadline(time.Now().Add(8 * time.Second))
	parts, err := s.conn.ReadPartitions()
	if err != nil {
		var ke kafka.Error
		if errors.As(err, &ke) && (ke == kafka.TopicAuthorizationFailed || ke == kafka.ClusterAuthorizationFailed) {
			return stageErr("Login accepted but this account may not list topics. Grant Describe on the cluster or topics.")
		}
		return stageErr("A read-only metadata request failed: %s", s.p.Scrub(firstLine(err)))
	}
	seen := map[string]bool{}
	for _, p := range parts {
		seen[p.Topic] = true
	}
	s.topics = len(seen)
	if brokers, err := s.conn.Brokers(); err == nil {
		s.brokers = len(brokers)
	}
	return nil
}

func (s *kafkaSession) Details(ctx context.Context) (Details, *StageError) {
	// The Kafka protocol does not expose a plain version string; report the cluster shape instead.
	return Details{Info: fmt.Sprintf("%d broker(s), %d topic(s)", s.brokers, s.topics), Latency: s.latency}, nil
}
