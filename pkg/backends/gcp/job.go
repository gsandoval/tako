package gcp

import (
	"context"

	"github.com/gsandoval/tako/internal/trace"
)

// RunJob starts one execution of a Cloud Run Job (full resource name
// projects/P/locations/R/jobs/ID), handing the params over as the
// --tako-job-params container arg the generated job main decodes.
func (c *Client) RunJob(ctx context.Context, resource string, params []byte) error {
	args := []string{"--tako-job-params", string(params)}
	if tc, ok := trace.FromContext(ctx); ok {
		args = append(args, "--tako-job-trace", trace.FormatCloudTraceContext(tc.TraceID, tc.SpanID, tc.Sampled))
		if tc.ReqID != "" {
			args = append(args, "--tako-job-req", tc.ReqID)
		}
		if tc.SpanID != "" {
			args = append(args, "--tako-job-parent-span", tc.SpanID)
		}
	}
	if flags := trace.EncodeFlagsHeader(trace.PropagatedFlags(ctx)); flags != "" {
		args = append(args, "--tako-job-flags", flags)
	}
	body := map[string]any{
		"overrides": map[string]any{
			"containerOverrides": []any{
				map[string]any{"args": args},
			},
		},
	}
	return c.do(ctx, "POST", c.url(runHost, "/v2/"+resource+":run"), body, nil)
}
