package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// PurgeDatastore deletes every entity of the given kinds in one
// namespace — destroy's data pass, running only after the §4.6a typed
// confirmation. Kinds come from the app graph; the namespace is the
// env, so other envs' data is untouched. Returns entities deleted.
func (c *Client) PurgeDatastore(ctx context.Context, namespace string, kinds []string) (int, error) {
	deleted := 0
	base := c.url(datastoreHost, "/v1/projects/"+c.Project)
	partition := map[string]any{"projectId": c.Project, "namespaceId": namespace}
	for _, kind := range kinds {
		for {
			body := map[string]any{
				"partitionId": partition,
				"query": map[string]any{
					"kind":       []map[string]any{{"name": kind}},
					"projection": []map[string]any{{"property": map[string]any{"name": "__key__"}}},
					"limit":      500,
				},
			}
			var out struct {
				Batch struct {
					EntityResults []struct {
						Entity struct {
							Key map[string]any `json:"key"`
						} `json:"entity"`
					} `json:"entityResults"`
				} `json:"batch"`
			}
			if err := c.do(ctx, http.MethodPost, base+":runQuery", body, &out); err != nil {
				return deleted, Classify(err, c.Project, "list datastore entities", "roles/datastore.owner")
			}
			if len(out.Batch.EntityResults) == 0 {
				break
			}
			mutations := make([]map[string]any, 0, len(out.Batch.EntityResults))
			for _, er := range out.Batch.EntityResults {
				mutations = append(mutations, map[string]any{"delete": er.Entity.Key})
			}
			commit := map[string]any{"mode": "NON_TRANSACTIONAL", "mutations": mutations}
			if err := c.do(ctx, http.MethodPost, base+":commit", commit, nil); err != nil {
				return deleted, Classify(err, c.Project, "delete datastore entities", "roles/datastore.owner")
			}
			deleted += len(mutations)
		}
	}
	return deleted, nil
}

// KindsInNamespace lists the entity kinds present in one namespace via
// the __kind__ pseudo-kind — how prune spots data whose declaration
// disappeared from code.
func (c *Client) KindsInNamespace(ctx context.Context, namespace string) ([]string, error) {
	body := map[string]any{
		"partitionId": map[string]any{"projectId": c.Project, "namespaceId": namespace},
		"query":       map[string]any{"kind": []map[string]any{{"name": "__kind__"}}},
	}
	var out struct {
		Batch struct {
			EntityResults []struct {
				Entity struct {
					Key struct {
						Path []struct {
							Name string `json:"name"`
						} `json:"path"`
					} `json:"key"`
				} `json:"entity"`
			} `json:"entityResults"`
		} `json:"batch"`
	}
	if err := c.do(ctx, http.MethodPost, c.url(datastoreHost, "/v1/projects/"+c.Project+":runQuery"), body, &out); err != nil {
		return nil, Classify(err, c.Project, "list datastore kinds", "roles/datastore.viewer")
	}
	var kinds []string
	for _, er := range out.Batch.EntityResults {
		for _, p := range er.Entity.Key.Path {
			if p.Name != "" && !strings.HasPrefix(p.Name, "__") {
				kinds = append(kinds, p.Name)
			}
		}
	}
	return kinds, nil
}

// CountKind counts one kind's entities in a namespace (prune --data's
// confirmation listing). Uses the aggregation query, capped implicitly
// by Datastore.
func (c *Client) CountKind(ctx context.Context, namespace, kind string) (int, error) {
	body := map[string]any{
		"partitionId": map[string]any{"projectId": c.Project, "namespaceId": namespace},
		"aggregationQuery": map[string]any{
			"nestedQuery":  map[string]any{"kind": []map[string]any{{"name": kind}}},
			"aggregations": []map[string]any{{"count": map[string]any{}, "alias": "n"}},
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
	if err := c.do(ctx, http.MethodPost, c.url(datastoreHost, "/v1/projects/"+c.Project+":runAggregationQuery"), body, &out); err != nil {
		return 0, Classify(err, c.Project, "count datastore entities", "roles/datastore.viewer")
	}
	for _, r := range out.Batch.AggregationResults {
		if v, ok := r.AggregateProperties["n"]; ok {
			var n int
			fmt.Sscanf(v.IntegerValue, "%d", &n)
			return n, nil
		}
	}
	return 0, nil
}

// Namespaces lists the database's namespaces via the __namespace__
// pseudo-kind ("" is the default namespace, which tako never writes).
func (c *Client) Namespaces(ctx context.Context) ([]string, error) {
	body := map[string]any{
		"query": map[string]any{"kind": []map[string]any{{"name": "__namespace__"}}},
	}
	var out struct {
		Batch struct {
			EntityResults []struct {
				Entity struct {
					Key struct {
						Path []struct {
							Name string `json:"name"`
						} `json:"path"`
					} `json:"key"`
				} `json:"entity"`
			} `json:"entityResults"`
		} `json:"batch"`
	}
	if err := c.do(ctx, http.MethodPost, c.url(datastoreHost, "/v1/projects/"+c.Project+":runQuery"), body, &out); err != nil {
		return nil, Classify(err, c.Project, "list datastore namespaces", "roles/datastore.viewer")
	}
	var names []string
	for _, er := range out.Batch.EntityResults {
		for _, p := range er.Entity.Key.Path {
			if p.Name != "" {
				names = append(names, p.Name)
			}
		}
	}
	return names, nil
}
