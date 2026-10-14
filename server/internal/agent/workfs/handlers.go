package workfs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/nexio-developer-group/nexmesh/server/internal/agentproto"
)

type installer interface {
	Handle(method string, h agentproto.Handler)
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return v, agentproto.Errf("bad_params", "Invalid request parameters.")
		}
	}
	return v, nil
}

// Install registers the fs.* methods on an agent.
func (f *FS) Install(a installer) {
	type pathP struct {
		Path string `json:"path"`
	}
	a.Handle("fs.list", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[pathP](raw)
		if err != nil {
			return nil, err
		}
		es, err := f.List(p.Path)
		return map[string]any{"entries": es}, err
	})
	a.Handle("fs.stat", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[pathP](raw)
		if err != nil {
			return nil, err
		}
		return f.Stat(p.Path)
	})
	a.Handle("fs.read", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[struct {
			Path     string `json:"path"`
			Encoding string `json:"encoding"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if p.Encoding == "base64" {
			b, mt, err := f.ReadBytes(p.Path)
			if err != nil {
				return nil, err
			}
			return map[string]any{"path": p.Path, "content": base64.StdEncoding.EncodeToString(b), "encoding": "base64", "size": len(b), "modTime": mt}, nil
		}
		return f.Read(p.Path)
	})
	a.Handle("fs.write", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[struct {
			Path     string    `json:"path"`
			Content  string    `json:"content"`
			Encoding string    `json:"encoding"`
			Expected time.Time `json:"expectedModTime"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if p.Encoding == "base64" {
			b, err := base64.StdEncoding.DecodeString(p.Content)
			if err != nil {
				return nil, agentproto.Errf("bad_params", "Content is not valid base64.")
			}
			return f.WriteBytes(p.Path, b, p.Expected, 0o644)
		}
		return f.Write(p.Path, p.Content, p.Expected, 0o644)
	})
	a.Handle("fs.mkdir", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[pathP](raw)
		if err != nil {
			return nil, err
		}
		return struct{}{}, f.Mkdir(p.Path)
	})
	a.Handle("fs.rename", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[struct{ From, To string }](raw)
		if err != nil {
			return nil, err
		}
		return struct{}{}, f.Rename(p.From, p.To)
	})
	a.Handle("fs.delete", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[struct {
			Path      string `json:"path"`
			Recursive bool   `json:"recursive"`
		}](raw)
		if err != nil {
			return nil, err
		}
		return struct{}{}, f.Delete(p.Path, p.Recursive)
	})
	a.Handle("fs.search", func(_ context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[struct {
			Query   string `json:"query"`
			Content bool   `json:"content"`
			Limit   int    `json:"limit"`
		}](raw)
		if err != nil {
			return nil, err
		}
		hits, trunc, err := f.Search(p.Query, p.Content, p.Limit)
		return map[string]any{"hits": hits, "truncated": trunc}, err
	})
}
