package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// EntityStore implements backends.Entity on Firestore in Datastore mode.
//
// Layout: one Datastore entity per document. The raw JSON doc rides an
// unindexed blob property "_doc" so Get returns byte-identical content;
// every top-level scalar JSON field is additionally stored as an indexed,
// typed property (string / double / boolean / timestamp) so filters and
// ordering run natively with the same semantics the local backend gives
// them. Env isolation is the namespace (namespace = env). Strong
// consistency comes from Datastore mode, so put-then-query reads its
// writes like the local backend does.
type EntityStore struct {
	c *Client
}

func NewEntityStore(c *Client) *EntityStore { return &EntityStore{c: c} }

const docProp = "_doc"

func (s *EntityStore) url(verb string) string {
	return s.c.url(datastoreHost, "/v1/projects/"+s.c.Opts.Project+":"+verb)
}

func (s *EntityStore) partition() map[string]any {
	return map[string]any{"projectId": s.c.Opts.Project, "namespaceId": s.c.Opts.Env}
}

func (s *EntityStore) key(kind, id string) map[string]any {
	return map[string]any{
		"partitionId": s.partition(),
		"path":        []map[string]any{{"kind": kind, "name": id}},
	}
}

// value maps a normalized JSON value to a typed Datastore value.
// RFC 3339 strings become timestamps so instants order across zones —
// the same rule the local backend's comparator applies.
func value(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case string:
		if _, err := time.Parse(time.RFC3339, t); err == nil {
			return map[string]any{"timestampValue": t}, true
		}
		return map[string]any{"stringValue": t}, true
	case float64:
		return map[string]any{"doubleValue": t}, true
	case bool:
		return map[string]any{"booleanValue": t}, true
	case nil:
		return map[string]any{"nullValue": "NULL_VALUE"}, true
	default: // arrays and objects are not queryable in v0
		return nil, false
	}
}

// entity builds the Datastore entity: the document itself in one
// excluded blob, plus a mirrored property per scalar so queries have
// something to filter and order on.
//
// Which of those mirrors are INDEXED is the whole of FL20. It used to
// be all of them, because the write path could not see the app's
// entity:"index" tags — the contract carried bytes alone. So an
// untagged field was indexed anyway, and Datastore caps an indexed
// string at 1500 bytes: an app failed to write a paragraph it had
// never declared queryable, citing an index it had never asked for.
//
// Now the queryable set arrives with the document and everything else
// is excluded. A mirror is still written for every scalar — excluded
// properties are readable and cost nothing to keep — so nothing about
// reads changes; they are simply no longer in an index.
func (s *EntityStore) entity(kind, id string, doc []byte, indexed []string) (map[string]any, error) {
	props := map[string]any{
		docProp: map[string]any{
			"blobValue":          base64.StdEncoding.EncodeToString(doc),
			"excludeFromIndexes": true,
		},
	}
	var fields map[string]any
	if err := json.Unmarshal(doc, &fields); err != nil {
		return nil, fmt.Errorf("entity doc is not a JSON object: %w", err)
	}
	queryable := make(map[string]bool, len(indexed))
	for _, name := range indexed {
		queryable[name] = true
	}
	for name, v := range fields {
		if name == docProp {
			continue
		}
		dv, ok := value(v)
		if !ok {
			continue
		}
		if !queryable[name] {
			// Copied, because value() returns a fresh map per call but
			// the intent is worth being explicit about: stored, never
			// indexed, no cap.
			dv["excludeFromIndexes"] = true
		} else if str, isString := fields[name].(string); isString && len(str) > backends.MaxIndexedBytes {
			// Caught HERE rather than at the API, because Datastore's
			// version of this names a property and a limit and leaves
			// the reader to work out that a struct tag put it there.
			return nil, backends.IndexedTooLong(kind, name, len(str))
		}
		props[name] = dv
	}
	return map[string]any{"key": s.key(kind, id), "properties": props}, nil
}

// docOf recovers the raw document from a lookup/query result entity.
func docOf(entity map[string]any) ([]byte, error) {
	props, _ := entity["properties"].(map[string]any)
	dp, _ := props[docProp].(map[string]any)
	b64, _ := dp["blobValue"].(string)
	if b64 == "" {
		return nil, fmt.Errorf("entity has no %s property — not written by tako", docProp)
	}
	return base64.StdEncoding.DecodeString(b64)
}

func idOf(entity map[string]any) string {
	key, _ := entity["key"].(map[string]any)
	path, _ := key["path"].([]any)
	if len(path) == 0 {
		return ""
	}
	last, _ := path[len(path)-1].(map[string]any)
	name, _ := last["name"].(string)
	return name
}

type lookupResp struct {
	Found []struct {
		Entity map[string]any `json:"entity"`
	} `json:"found"`
	Missing []any `json:"missing"`
}

func (s *EntityStore) lookup(ctx context.Context, kind, id, tx string) (map[string]any, error) {
	body := map[string]any{"keys": []any{s.key(kind, id)}}
	if tx != "" {
		body["readOptions"] = map[string]any{"transaction": tx}
	}
	var out lookupResp
	if err := s.c.do(ctx, "POST", s.url("lookup"), body, &out); err != nil {
		return nil, err
	}
	if len(out.Found) == 0 {
		return nil, backends.ErrNotFound
	}
	return out.Found[0].Entity, nil
}

func (s *EntityStore) Get(ctx context.Context, kind, id string) ([]byte, error) {
	entity, err := s.lookup(ctx, kind, id, "")
	if err != nil {
		return nil, err
	}
	return docOf(entity)
}

func (s *EntityStore) Exists(ctx context.Context, kind, id string) (bool, error) {
	_, err := s.lookup(ctx, kind, id, "")
	if err == backends.ErrNotFound {
		return false, nil
	}
	return err == nil, err
}

func (s *EntityStore) commit(ctx context.Context, tx string, mutations []map[string]any) error {
	body := map[string]any{"mode": "NON_TRANSACTIONAL", "mutations": mutations}
	if tx != "" {
		body["mode"], body["transaction"] = "TRANSACTIONAL", tx
	}
	return s.c.do(ctx, "POST", s.url("commit"), body, nil)
}

func (s *EntityStore) Put(ctx context.Context, kind, id string, doc []byte, indexed []string) error {
	entity, err := s.entity(kind, id, doc, indexed)
	if err != nil {
		return err
	}
	return s.commit(ctx, "", []map[string]any{{"upsert": entity}})
}

func (s *EntityStore) Delete(ctx context.Context, kind, id string) error {
	return s.commit(ctx, "", []map[string]any{{"delete": s.key(kind, id)}})
}

func (s *EntityStore) Update(ctx context.Context, kind, id string, indexed []string, mutate func(doc []byte) ([]byte, error)) error {
	return s.Tx(ctx, kind, indexed, func(tx backends.EntityTxOps) error {
		doc, err := tx.Get(id)
		if err != nil {
			return err
		}
		next, err := mutate(doc)
		if err != nil {
			return err
		}
		return tx.Put(id, next)
	})
}

// entityTx buffers writes; Datastore applies them atomically at commit.
type entityTx struct {
	s         *EntityStore
	ctx       context.Context
	kind, tx  string
	indexed   []string
	mutations []map[string]any
}

func (t *entityTx) Get(id string) ([]byte, error) {
	entity, err := t.s.lookup(t.ctx, t.kind, id, t.tx)
	if err != nil {
		return nil, err
	}
	return docOf(entity)
}

func (t *entityTx) Put(id string, doc []byte) error {
	entity, err := t.s.entity(t.kind, id, doc, t.indexed)
	if err != nil {
		return err
	}
	t.mutations = append(t.mutations, map[string]any{"upsert": entity})
	return nil
}

func (t *entityTx) Delete(id string) error {
	t.mutations = append(t.mutations, map[string]any{"delete": t.s.key(t.kind, id)})
	return nil
}

func (s *EntityStore) Tx(ctx context.Context, kind string, indexed []string, fn func(tx backends.EntityTxOps) error) error {
	var begin struct {
		Transaction string `json:"transaction"`
	}
	if err := s.c.do(ctx, "POST", s.url("beginTransaction"), map[string]any{}, &begin); err != nil {
		return err
	}
	t := &entityTx{s: s, ctx: ctx, kind: kind, indexed: indexed, tx: begin.Transaction}
	if err := fn(t); err != nil {
		_ = s.c.do(ctx, "POST", s.url("rollback"), map[string]any{"transaction": begin.Transaction}, nil)
		return err
	}
	if len(t.mutations) == 0 {
		_ = s.c.do(ctx, "POST", s.url("rollback"), map[string]any{"transaction": begin.Transaction}, nil)
		return nil
	}
	return s.commit(ctx, begin.Transaction, t.mutations)
}

var datastoreOps = map[string]string{
	"=": "EQUAL", "<": "LESS_THAN", "<=": "LESS_THAN_OR_EQUAL",
	">": "GREATER_THAN", ">=": "GREATER_THAN_OR_EQUAL",
}

// normalize round-trips a filter value through JSON so it types the way
// documents do (ints → float64, time.Time → RFC 3339 string).
func normalize(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

// filteredQuery builds the kind-and-filter half of a Datastore query.
//
// Shared by Query and Count rather than written twice: a count that
// filters differently from the page it describes is worse than no count,
// because a pager would confidently render "page 3 of 2".
func filteredQuery(kind string, fs []backends.Filter) (map[string]any, error) {
	query := map[string]any{"kind": []map[string]any{{"name": kind}}}
	var filters []map[string]any
	for _, f := range fs {
		op, ok := datastoreOps[f.Op]
		if !ok {
			return nil, fmt.Errorf("unsupported filter op %q", f.Op)
		}
		dv, ok := value(normalize(f.Value))
		if !ok {
			return nil, fmt.Errorf("unsupported filter value for %s", f.Field)
		}
		filters = append(filters, map[string]any{"propertyFilter": map[string]any{
			"property": map[string]any{"name": f.Field}, "op": op, "value": dv,
		}})
	}
	switch len(filters) {
	case 0:
	case 1:
		query["filter"] = filters[0]
	default:
		query["filter"] = map[string]any{"compositeFilter": map[string]any{"op": "AND", "filters": filters}}
	}
	return query, nil
}

// Page returns one page and Datastore's own cursor to resume from.
//
// The cursor was already in hand and thrown away: Query reads endCursor
// on every batch and uses it internally to drain them, then drops it at
// the API boundary. What this adds is not the cursor but the DISCIPLINE
// around it.
//
// An explicit order is required, and that is not a nicety here. When
// q.Order is empty, Query sorts client-side — it cannot always push the
// default id order down beside an inequality filter — and the server's
// cursor is then a position in the order DATASTORE used, not the one
// the caller asked for. Handed back, it would silently skip or repeat
// rows. The refusal is identical on local, so a query cannot page
// correctly in development and lie in production.
//
// Unlike Query, this does not drain batches: one call, one page.
func (s *EntityStore) Page(ctx context.Context, kind string, q backends.EntityQuery, after string) ([][]byte, string, error) {
	if q.Order == "" {
		return nil, "", backends.ErrPageNeedsOrder
	}
	if q.Limit <= 0 {
		return nil, "", backends.ErrPageNeedsLimit
	}
	query, err := filteredQuery(kind, q.Filters)
	if err != nil {
		return nil, "", err
	}
	field, dir := q.Order, "ASCENDING"
	if field[0] == '-' {
		field, dir = field[1:], "DESCENDING"
	}
	// __key__ ascending is the same tiebreak Query pushes down and the
	// same one local applies, so a page boundary that falls inside a run
	// of equal sort values resumes in one agreed order rather than two.
	query["order"] = []map[string]any{
		{"property": map[string]any{"name": field}, "direction": dir},
		{"property": map[string]any{"name": "__key__"}, "direction": "ASCENDING"},
	}
	query["limit"] = q.Limit
	if after != "" {
		query["startCursor"] = after
	}

	var out struct {
		Batch struct {
			EntityResults []struct {
				Entity map[string]any `json:"entity"`
			} `json:"entityResults"`
			MoreResults string `json:"moreResults"`
			EndCursor   string `json:"endCursor"`
		} `json:"batch"`
	}
	body := map[string]any{"partitionId": s.partition(), "query": query}
	if err := s.c.do(ctx, "POST", s.url("runQuery"), body, &out); err != nil {
		return nil, "", err
	}
	docs := make([][]byte, 0, len(out.Batch.EntityResults))
	for _, er := range out.Batch.EntityResults {
		doc, err := docOf(er.Entity)
		if err != nil {
			return nil, "", err
		}
		docs = append(docs, doc)
	}
	// A cursor is returned only when there is more to read. Datastore
	// says so with MORE_RESULTS_AFTER_LIMIT; every other answer —
	// NO_MORE_RESULTS above all — means this was the last page, and
	// handing back a cursor then would invite one round trip that can
	// only come back empty.
	next := ""
	if out.Batch.MoreResults == "MORE_RESULTS_AFTER_LIMIT" && len(docs) > 0 {
		next = out.Batch.EndCursor
	}
	return docs, next, nil
}

// Count answers server-side, through runAggregationQuery — a sibling
// endpoint of the runQuery this client already speaks, taking the same
// query object wrapped in an aggregation.
//
// That matters for cost as much as correctness: Datastore bills an
// aggregation per index entry scanned rather than per entity returned,
// so counting ten thousand matches does not read ten thousand entities.
// Doing it client-side would have meant paging the whole result set to
// throw it away.
//
// The query carries NO order and NO limit. A limit would cap the total
// at a page size, and an order would exclude entities missing the sort
// property — Datastore's index-backed sort only sees entities that have
// the field, so an ordered count can silently answer low.
func (s *EntityStore) Count(ctx context.Context, kind string, q backends.EntityQuery) (int64, error) {
	nested, err := filteredQuery(kind, q.Filters)
	if err != nil {
		return 0, err
	}
	body := map[string]any{
		"partitionId": s.partition(),
		"aggregationQuery": map[string]any{
			"nestedQuery":  nested,
			"aggregations": []map[string]any{{"count": map[string]any{}, "alias": "total"}},
		},
	}
	var out struct {
		Batch struct {
			AggregationResults []struct {
				AggregateProperties map[string]struct {
					IntegerValue string `json:"integerValue"`
				} `json:"aggregateProperties"`
			} `json:"aggregationResults"`
		} `json:"batch"`
	}
	if err := s.c.do(ctx, "POST", s.url("runAggregationQuery"), body, &out); err != nil {
		return 0, err
	}
	if len(out.Batch.AggregationResults) == 0 {
		// An empty kind still answers with a result carrying zero. No
		// results at all means the shape changed under us, and guessing
		// zero would report "nothing matched" for "we did not ask".
		return 0, fmt.Errorf("datastore returned no aggregation result for kind %q", kind)
	}
	prop, ok := out.Batch.AggregationResults[0].AggregateProperties["total"]
	if !ok {
		return 0, fmt.Errorf("datastore aggregation carried no %q property for kind %q", "total", kind)
	}
	// int64 crosses Datastore's JSON as a STRING, so this is a parse and
	// not a cast.
	n, err := strconv.ParseInt(prop.IntegerValue, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("datastore count %q is not a number: %w", prop.IntegerValue, err)
	}
	return n, nil
}

func (s *EntityStore) Query(ctx context.Context, kind string, q backends.EntityQuery) ([][]byte, error) {
	query, err := filteredQuery(kind, q.Filters)
	if err != nil {
		return nil, err
	}

	// An explicit order pushes down (with __key__ as the deterministic
	// tiebreak, like local's id tiebreak). The contract's default id
	// order can't always push down next to inequality filters, so it
	// sorts client-side instead — as does its limit, which must apply
	// in id order.
	pushLimit := false
	if q.Order != "" {
		field, dir := q.Order, "ASCENDING"
		if field[0] == '-' {
			field, dir = field[1:], "DESCENDING"
		}
		query["order"] = []map[string]any{
			{"property": map[string]any{"name": field}, "direction": dir},
			{"property": map[string]any{"name": "__key__"}, "direction": "ASCENDING"},
		}
		if q.Limit > 0 {
			query["limit"] = q.Limit
			pushLimit = true
		}
	}

	type result struct {
		id  string
		doc []byte
	}
	var results []result
	cursor := ""
	for {
		if cursor != "" {
			query["startCursor"] = cursor
		}
		var out struct {
			Batch struct {
				EntityResults []struct {
					Entity map[string]any `json:"entity"`
				} `json:"entityResults"`
				MoreResults string `json:"moreResults"`
				EndCursor   string `json:"endCursor"`
			} `json:"batch"`
		}
		body := map[string]any{"partitionId": s.partition(), "query": query}
		if err := s.c.do(ctx, "POST", s.url("runQuery"), body, &out); err != nil {
			return nil, err
		}
		for _, er := range out.Batch.EntityResults {
			doc, err := docOf(er.Entity)
			if err != nil {
				return nil, err
			}
			results = append(results, result{idOf(er.Entity), doc})
		}
		if out.Batch.MoreResults != "NOT_FINISHED" || out.Batch.EndCursor == cursor {
			break
		}
		cursor = out.Batch.EndCursor
	}

	if q.Order == "" {
		sort.Slice(results, func(i, j int) bool { return results[i].id < results[j].id })
	}
	if q.Limit > 0 && !pushLimit && len(results) > q.Limit {
		results = results[:q.Limit]
	}
	docs := make([][]byte, len(results))
	for i, r := range results {
		docs[i] = r.doc
	}
	return docs, nil
}
