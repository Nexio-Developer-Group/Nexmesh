package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Operators that run JavaScript or write: refused anywhere in a filter.
var mongoBlockedKeys = map[string]bool{"$where": true, "$function": true, "$accumulator": true, "$out": true, "$merge": true}

func scanKeys(v any, depth int) *InspectError {
	if depth > 20 {
		return ierr("bad_args", "The filter is nested too deeply.")
	}
	switch x := v.(type) {
	case bson.D:
		for _, e := range x {
			if mongoBlockedKeys[e.Key] {
				return ierr("read_only", "The %s operator is not allowed in read-only mode.", e.Key)
			}
			if err := scanKeys(e.Value, depth+1); err != nil {
				return err
			}
		}
	case bson.A:
		for _, e := range x {
			if err := scanKeys(e, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseJSONDoc(s, what string) (bson.D, *InspectError) {
	if strings.TrimSpace(s) == "" {
		return bson.D{}, nil
	}
	var d bson.D
	if err := bson.UnmarshalExtJSON([]byte(s), false, &d); err != nil {
		return nil, ierr("bad_args", "The %s is not valid JSON: %s", what, firstLine(err))
	}
	if e := scanKeys(d, 0); e != nil {
		return nil, e
	}
	return d, nil
}

func inspectMongo(ctx context.Context, t *Target, p Params, req InspectRequest) (*InspectResult, *InspectError) {
	s0, oe := openMongo(t, p)
	if oe != nil {
		return nil, connectErr(oe)
	}
	s := s0.(*mongoSession)
	defer s.Close()
	if e := s.Authenticate(ctx); e != nil {
		return nil, connectErr(e)
	}
	dbName := argStr(req.Args, "database")
	if dbName == "" {
		dbName = str(p.Fields, "database")
	}
	need := func(names ...string) *InspectError {
		for _, n := range names {
			if n == "database" && dbName == "" || n != "database" && argStr(req.Args, n) == "" {
				return ierr("bad_args", "%s is required.", n)
			}
		}
		return nil
	}
	limit := limitArg(req.Args)
	res := &InspectResult{Rows: [][]any{}}
	fail := func(err error) (*InspectResult, *InspectError) {
		return nil, ierr("query_failed", "%s", p.Scrub(firstLine(err)))
	}

	switch req.Op {
	case "databases":
		names, err := s.c.ListDatabaseNames(ctx, bson.D{}, options.ListDatabases().SetAuthorizedDatabases(true))
		if err != nil {
			return fail(err)
		}
		sort.Strings(names)
		res.Columns = []string{"database"}
		for _, n := range names {
			res.Rows = append(res.Rows, []any{n})
		}
	case "collections":
		if e := need("database"); e != nil {
			return nil, e
		}
		names, err := s.c.Database(dbName).ListCollectionNames(ctx, bson.D{})
		if err != nil {
			return fail(err)
		}
		sort.Strings(names)
		res.Columns = []string{"collection", "approx_documents"}
		for _, n := range names {
			cnt, err := s.c.Database(dbName).Collection(n).EstimatedDocumentCount(ctx)
			var v any = cnt
			if err != nil {
				v = nil
			}
			res.Rows = append(res.Rows, []any{n, v})
		}
	case "schema":
		if e := need("database", "collection"); e != nil {
			return nil, e
		}
		cur, err := s.c.Database(dbName).Collection(argStr(req.Args, "collection")).Find(ctx, bson.D{}, options.Find().SetLimit(50))
		if err != nil {
			return fail(err)
		}
		defer cur.Close(ctx)
		types := map[string]map[string]bool{}
		n := 0
		for cur.Next(ctx) {
			var d bson.M
			if err := cur.Decode(&d); err != nil {
				continue
			}
			n++
			for k, v := range d {
				if types[k] == nil {
					types[k] = map[string]bool{}
				}
				types[k][fmt.Sprintf("%T", v)] = true
			}
		}
		keys := make([]string, 0, len(types))
		for k := range types {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		res.Columns = []string{"field", "types"}
		for _, k := range keys {
			ts := make([]string, 0)
			for t := range types[k] {
				ts = append(ts, t)
			}
			sort.Strings(ts)
			res.Rows = append(res.Rows, []any{k, strings.Join(ts, ", ")})
		}
		res.Note = fmt.Sprintf("Inferred from a sample of %d documents.", n)
	case "count":
		if e := need("database", "collection"); e != nil {
			return nil, e
		}
		filter, e := parseJSONDoc(argStr(req.Args, "filter"), "filter")
		if e != nil {
			return nil, e
		}
		n, err := s.c.Database(dbName).Collection(argStr(req.Args, "collection")).CountDocuments(ctx, filter, options.Count())
		if err != nil {
			return fail(err)
		}
		res.Columns = []string{"count"}
		res.Rows = [][]any{{n}}
	case "find":
		if e := need("database", "collection"); e != nil {
			return nil, e
		}
		filter, e := parseJSONDoc(argStr(req.Args, "filter"), "filter")
		if e != nil {
			return nil, e
		}
		sortDoc, e := parseJSONDoc(argStr(req.Args, "sort"), "sort")
		if e != nil {
			return nil, e
		}
		opts := options.Find().SetLimit(int64(limit + 1))
		if len(sortDoc) > 0 {
			opts.SetSort(sortDoc)
		}
		cur, err := s.c.Database(dbName).Collection(argStr(req.Args, "collection")).Find(ctx, filter, opts)
		if err != nil {
			return fail(err)
		}
		defer cur.Close(ctx)
		res.Columns = []string{"_id", "document"}
		var pl payload
		for cur.Next(ctx) {
			if len(res.Rows) >= limit {
				res.Truncated = true
				res.Note = "Showing the first documents only. Add a filter or lower the limit."
				break
			}
			var d bson.D
			if err := cur.Decode(&d); err != nil {
				return fail(err)
			}
			doc, _ := bson.MarshalExtJSON(d, false, false)
			var id any
			for _, e := range d {
				if e.Key == "_id" {
					b, _ := json.Marshal(e.Value)
					id = string(b)
				}
			}
			row := []any{cell(id), cell(string(doc))}
			if !pl.add(row) {
				res.Truncated = true
				res.Note = "The result is large; showing the documents that fit."
				break
			}
			res.Rows = append(res.Rows, row)
		}
		if err := cur.Err(); err != nil && !res.Truncated {
			return fail(err)
		}
	}
	return res, nil
}
