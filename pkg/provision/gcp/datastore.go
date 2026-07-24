package gcp

import (
	"context"
	"net/http"
)

// PurgeDatastore deletes every entity of the given kinds in one
// namespace — destroy's data pass, running only after the §4.6a typed
// confirmation. Kinds come from the app graph; the namespace is the
// env, so other envs' data is untouched. Returns entities deleted.
func (c *Client) PurgeDatastore(ctx context.Context, namespace string, kinds []string) (int, error) {
	deleted := 0
	base := "https://datastore.googleapis.com/v1/projects/" + c.Project
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
