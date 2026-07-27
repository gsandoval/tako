package gcp

import (
	"context"
)

// RunJob starts one execution of a Cloud Run Job (full resource name
// projects/P/locations/R/jobs/ID), handing the params over as the
// --tako-job-params container arg the generated job main decodes.
func (c *Client) RunJob(ctx context.Context, resource string, params []byte) error {
	body := map[string]any{
		"overrides": map[string]any{
			"containerOverrides": []any{
				map[string]any{"args": []string{"--tako-job-params", string(params)}},
			},
		},
	}
	return c.do(ctx, "POST", "https://run.googleapis.com/v2/"+resource+":run", body, nil)
}
