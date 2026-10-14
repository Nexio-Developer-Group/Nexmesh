package probe

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// RabbitMQ is inspected through its Management API (queue depth and consumers
// are not available over AMQP without declaring queues). The URL is optional in
// the connection, so the inspector explains what to set when it is missing.
func inspectRabbit(ctx context.Context, t *Target, p Params, req InspectRequest) (*InspectResult, *InspectError) {
	base := str(p.Fields, "managementUrl")
	if base == "" {
		return nil, ierr("needs_management_url", "Set the Management API URL (for example https://host:15671) under Advanced in this connection to inspect queues.")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, ierr("bad_connection", "The Management API URL must start with http:// or https://.")
	}
	path := "/api/overview"
	if req.Op == "queues" {
		path = "/api/queues"
		if v := argStr(req.Args, "vhost"); v != "" {
			path += "/" + url.PathEscape(v)
		}
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = "disable_stats=false&columns=name,vhost,state,messages,messages_ready,messages_unacknowledged,consumers&page=1&page_size=200"
	if req.Op == "overview" {
		u.RawQuery = ""
	}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return p.Guard.DialContext(ctx, network, addr)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 8 * time.Second,
		TLSClientConfig:       t.TLS,
	}
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	hr, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	hr.SetBasicAuth(str(p.Fields, "username"), str(p.Fields, "password"))
	resp, err := cl.Do(hr)
	if err != nil {
		return nil, ierr("unreachable", "The Management API did not answer: %s", p.Scrub(firstLine(err)))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxPayload))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ierr("permission", "The Management API refused these credentials. The account needs the monitoring tag.")
	case resp.StatusCode != http.StatusOK:
		return nil, ierr("query_failed", "The Management API answered %d.", resp.StatusCode)
	}
	res := &InspectResult{Rows: [][]any{}}
	if req.Op == "overview" {
		var o struct {
			Version string `json:"rabbitmq_version"`
			Cluster string `json:"cluster_name"`
			Totals  struct {
				Messages int `json:"messages"`
				Ready    int `json:"messages_ready"`
				Unacked  int `json:"messages_unacknowledged"`
			} `json:"queue_totals"`
			Objects struct {
				Queues, Consumers, Connections, Channels, Exchanges int
			} `json:"object_totals"`
		}
		if err := json.Unmarshal(body, &o); err != nil {
			return nil, ierr("query_failed", "The Management API returned something unexpected.")
		}
		res.Columns = []string{"property", "value"}
		res.Rows = [][]any{{"version", o.Version}, {"cluster", o.Cluster}, {"queues", o.Objects.Queues}, {"consumers", o.Objects.Consumers},
			{"connections", o.Objects.Connections}, {"messages", o.Totals.Messages}, {"ready", o.Totals.Ready}, {"unacknowledged", o.Totals.Unacked}}
		return res, nil
	}
	var qs []struct {
		Name      string `json:"name"`
		Vhost     string `json:"vhost"`
		State     string `json:"state"`
		Messages  int    `json:"messages"`
		Ready     int    `json:"messages_ready"`
		Unacked   int    `json:"messages_unacknowledged"`
		Consumers int    `json:"consumers"`
	}
	var paged struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal(body, &paged) == nil && paged.Items != nil {
		body, _ = json.Marshal(paged.Items)
	}
	if err := json.Unmarshal(body, &qs); err != nil {
		return nil, ierr("query_failed", "The Management API returned something unexpected.")
	}
	sort.Slice(qs, func(i, j int) bool { return qs[i].Name < qs[j].Name })
	res.Columns = []string{"queue", "vhost", "state", "messages", "ready", "unacknowledged", "consumers"}
	for i, q := range qs {
		if i >= limitArg(req.Args) {
			res.Truncated = true
			break
		}
		res.Rows = append(res.Rows, []any{cell(q.Name), cell(q.Vhost), q.State, q.Messages, q.Ready, q.Unacked, q.Consumers})
	}
	return res, nil
}
