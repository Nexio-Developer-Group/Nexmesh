package probe

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
)

func inspectKafka(ctx context.Context, t *Target, p Params, req InspectRequest) (*InspectResult, *InspectError) {
	s0, _ := openKafka(t, p)
	s := s0.(*kafkaSession)
	defer s.Close()
	if e := s.Authenticate(ctx); e != nil {
		return nil, connectErr(e)
	}
	res := &InspectResult{Rows: [][]any{}}
	fail := func(err error) (*InspectResult, *InspectError) {
		return nil, ierr("query_failed", "%s", p.Scrub(firstLine(err)))
	}
	topic := argStr(req.Args, "topic")
	// A metadata request that names a topic makes many brokers create it. Look
	// the topic up in the full list first, so a typo never creates anything.
	allParts := func() ([]kafka.Partition, *InspectError) {
		_ = s.conn.SetDeadline(time.Now().Add(8 * time.Second))
		parts, err := s.conn.ReadPartitions()
		if err != nil {
			return nil, ierr("query_failed", "%s", p.Scrub(firstLine(err)))
		}
		return parts, nil
	}
	topicParts := func() ([]kafka.Partition, *InspectError) {
		all, e := allParts()
		if e != nil {
			return nil, e
		}
		var out []kafka.Partition
		for _, pt := range all {
			if pt.Topic == topic {
				out = append(out, pt)
			}
		}
		if len(out) == 0 {
			return nil, ierr("not_found", "The topic %q does not exist.", topic)
		}
		return out, nil
	}

	switch req.Op {
	case "topics":
		parts, e := allParts()
		if e != nil {
			return nil, e
		}
		counts := map[string]int{}
		for _, pt := range parts {
			counts[pt.Topic]++
		}
		names := make([]string, 0, len(counts))
		for n := range counts {
			names = append(names, n)
		}
		sort.Strings(names)
		res.Columns = []string{"topic", "partitions"}
		for i, n := range names {
			if i >= MaxLimit {
				res.Truncated = true
				break
			}
			res.Rows = append(res.Rows, []any{n, counts[n]})
		}
	case "partitions":
		if topic == "" {
			return nil, ierr("bad_args", "Enter a topic.")
		}
		parts, e := topicParts()
		if e != nil {
			return nil, e
		}
		res.Columns = []string{"partition", "leader", "replicas", "first offset", "next offset", "messages"}
		for _, pt := range parts {
			lo, hi := int64(-1), int64(-1)
			if lc, err := s.dialer().DialLeader(ctx, "tcp", net.JoinHostPort(s.t.Host, strconv.Itoa(s.t.Port)), topic, pt.ID); err == nil {
				_ = lc.SetDeadline(time.Now().Add(6 * time.Second))
				if a, b, err := lc.ReadOffsets(); err == nil {
					lo, hi = a, b
				}
				_ = lc.Close()
			}
			msgs := any(nil)
			if lo >= 0 && hi >= lo {
				msgs = hi - lo
			}
			var rep []int
			for _, r := range pt.Replicas {
				rep = append(rep, r.ID)
			}
			res.Rows = append(res.Rows, []any{pt.ID, pt.Leader.ID, fmt.Sprint(rep), offsetCell(lo), offsetCell(hi), msgs})
		}
	case "sample":
		if topic == "" {
			return nil, ierr("bad_args", "Enter a topic.")
		}
		if _, e := topicParts(); e != nil {
			return nil, e
		}
		part := argInt(req.Args, "partition", 0)
		limit := limitArg(req.Args)
		if limit > 100 {
			limit = 100 // a sample, not a download
		}
		lc, err := s.dialer().DialLeader(ctx, "tcp", net.JoinHostPort(s.t.Host, strconv.Itoa(s.t.Port)), topic, part)
		if err != nil {
			return fail(err)
		}
		lo, hi, err := lc.ReadOffsets()
		_ = lc.Close()
		if err != nil {
			return fail(err)
		}
		res.Columns = []string{"offset", "timestamp", "key", "value"}
		if hi <= lo {
			res.Note = "The partition is empty."
			return res, nil
		}
		start := hi - int64(limit)
		if start < lo {
			start = lo
		}
		// A reader with no group never commits offsets: sampling leaves no trace in consumer lag.
		r := kafka.NewReader(kafka.ReaderConfig{
			Brokers: []string{net.JoinHostPort(s.t.Host, strconv.Itoa(s.t.Port))}, Topic: topic, Partition: part,
			Dialer: s.dialer(), MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 500 * time.Millisecond,
		})
		defer r.Close()
		if err := r.SetOffset(start); err != nil {
			return fail(err)
		}
		var pl payload
		for off := start; off < hi; off++ {
			rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			m, err := r.ReadMessage(rctx)
			cancel()
			if err != nil {
				res.Truncated = true
				res.Note = "Stopped early: " + p.Scrub(firstLine(err))
				break
			}
			row := []any{m.Offset, m.Time.UTC().Format(time.RFC3339), cell(m.Key), cell(m.Value)}
			if !pl.add(row) {
				res.Truncated = true
				break
			}
			res.Rows = append(res.Rows, row)
		}
		res.Note = fmt.Sprintf("The most recent messages of partition %d, oldest first. Nothing was acknowledged.", part) + res.Note
	}
	return res, nil
}

func offsetCell(o int64) any {
	if o < 0 {
		return nil
	}
	return o
}

// dialer rebuilds the authenticated dialer the session used.
func (s *kafkaSession) dialer() *kafka.Dialer {
	mech, _ := saslMechanism(s.p)
	clientID := str(s.p.Fields, "clientId")
	if clientID == "" {
		clientID = "nexmesh"
	}
	return &kafka.Dialer{
		Timeout: 6 * time.Second, ClientID: clientID, TLS: s.t.TLS, SASLMechanism: mech,
		DialFunc: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return s.p.Guard.DialContext(ctx, network, addr)
		},
	}
}
