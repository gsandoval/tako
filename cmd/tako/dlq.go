package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
	"github.com/gsandoval/tako/pkg/provision"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// runDLQ is `tako dlq [service] [--ack] [--project …] [--env …]`: the
// cloud counterpart of local's on-disk dead-letter JSON. Bare it lists
// every async service's dead-letter subscription with what's retained;
// with a service it prints the messages (peek — nothing is consumed);
// --ack drains what it printed.
func runDLQ(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	ack := false
	var service string
	for _, a := range rest {
		if a == "--ack" {
			ack = true
			continue
		}
		if service != "" {
			return fmt.Errorf("unknown argument %q (tako dlq accepts one service, --ack, --project, --env)", a)
		}
		service = a
	}
	if ack && service == "" {
		return fmt.Errorf("--ack needs a service (tako dlq <service> --ack)")
	}

	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	var asyncServices []string
	for _, h := range graph.Handlers {
		if h.Kind == "worker" || h.Kind == "subscribe" {
			asyncServices = append(asyncServices, h.Service)
		}
	}
	if len(asyncServices) == 0 {
		fmt.Println("this app has no workers or subscribers — nothing dead-letters")
		return nil
	}
	client, err := gcp.NewClient(context.Background(), t.project, t.region)
	if err != nil {
		return err
	}

	if service == "" {
		for _, svc := range asyncServices {
			msgs, err := client.PullDead(context.Background(), gcpb.DeadTopicName(t.env, svc), 100)
			if err != nil {
				return err
			}
			state := "empty"
			if n := len(msgs); n > 0 {
				state = fmt.Sprintf("%d message(s)", n)
				if n == 100 {
					state = "100+ messages"
				}
			}
			fmt.Printf("  %-28s %s\n", svc, state)
		}
		return nil
	}

	found := false
	for _, svc := range asyncServices {
		if svc == service || (provision.Config{Env: t.env}).Name(svc) == service {
			service, found = svc, true
			break
		}
	}
	if !found {
		return fmt.Errorf("no async service named %q — async services: %s", service, strings.Join(asyncServices, ", "))
	}
	sub := gcpb.DeadTopicName(t.env, service)
	msgs, err := client.PullDead(context.Background(), sub, 100)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		fmt.Printf("no dead-lettered messages for %s\n", service)
		return nil
	}
	var ackIDs []string
	for _, m := range msgs {
		reason := m.Reason
		if reason == "" {
			// No tako attribute: the broker forwarded it after exhausting
			// delivery attempts (the runtime only stamps Permanent ones).
			reason = "delivery attempts exhausted"
		}
		fmt.Printf("  %s  %s\n    %s\n", m.Published.Local().Format("2006-01-02 15:04:05"), reason, m.Payload)
		ackIDs = append(ackIDs, m.AckID)
	}
	if ack {
		if err := client.AckDead(context.Background(), sub, ackIDs); err != nil {
			return err
		}
		fmt.Printf("✓ acknowledged %d message(s) — they will not be seen again\n", len(ackIDs))
	}
	return nil
}
