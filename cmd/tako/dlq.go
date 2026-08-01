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
	// --local is answered before parseTarget, which insists on a project:
	// asking for a GCP project to read files under .tako/data would be a
	// requirement invented by the parser, not by the task.
	local := false
	var kept []string
	for _, a := range args {
		if a == "--local" {
			local = true
			continue
		}
		kept = append(kept, a)
	}
	if local {
		return runDLQLocalArgs(kept)
	}
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	ack, replay, all := false, false, false
	var service, id string
	for _, a := range rest {
		switch {
		case a == "--ack":
			ack = true
			continue
		case a == "--replay":
			replay = true
			continue
		case a == "--all":
			all = true
			continue
		}
		if service == "" {
			service = a
			continue
		}
		if replay && id == "" {
			id = a
			continue
		}
		return fmt.Errorf("unknown argument %q (tako dlq accepts one service, --ack, --replay <id>, --replay --all, --project, --env)", a)
	}
	// Opposite verbs on the same message: --ack throws it away, --replay
	// puts it back. Refusing beats guessing which one was meant.
	if ack && replay {
		return fmt.Errorf("--ack and --replay do the opposite things to the same message — pick one")
	}
	if ack && service == "" {
		return fmt.Errorf("--ack needs a service (tako dlq <service> --ack)")
	}
	if replay && service == "" {
		return fmt.Errorf("--replay needs a service (tako dlq <service> --replay <id>)")
	}
	if replay && id == "" && !all {
		return fmt.Errorf("--replay needs an id, or --all (tako dlq <service> --replay <id>)")
	}
	if replay && id != "" && all {
		return fmt.Errorf("--replay takes an id or --all, not both")
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
	if replay {
		return replayDead(context.Background(), client, t, service, sub, msgs, id, all)
	}
	var ackIDs []string
	for _, m := range msgs {
		reason := m.Reason
		if reason == "" {
			// No tako attribute: the broker forwarded it after exhausting
			// delivery attempts (the runtime only stamps Permanent ones).
			reason = "delivery attempts exhausted"
		}
		fmt.Printf("  %s  %s  %s\n    %s\n", m.ID,
			m.Published.Local().Format("2006-01-02 15:04:05"), reason, m.Payload)
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

// pickDead resolves an id PREFIX to one message, the way git resolves a
// short SHA. Full ids are long and machine-shaped; asking a human to
// retype one exactly is a way of not being used.
func pickDead(msgs []gcp.DeadMessage, prefix string) (gcp.DeadMessage, error) {
	var hits []gcp.DeadMessage
	for _, m := range msgs {
		if strings.HasPrefix(m.ID, prefix) {
			hits = append(hits, m)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return gcp.DeadMessage{}, fmt.Errorf("no dead letter matching %q — tako dlq <service> lists what is there", prefix)
	default:
		return gcp.DeadMessage{}, fmt.Errorf("%q matches %d dead letters — use more characters", prefix, len(hits))
	}
}

// replayDead puts messages back into at-least-once delivery and then
// takes them off the DLQ — a MOVE. The ack comes AFTER the republish:
// a crash between the two duplicates a message, which the delivery
// contract already permits, where the other order would lose it.
func replayDead(ctx context.Context, client *gcp.Client, t target, service, sub string, msgs []gcp.DeadMessage, prefix string, all bool) error {
	chosen := msgs
	if !all {
		m, err := pickDead(msgs, prefix)
		if err != nil {
			return err
		}
		chosen = []gcp.DeadMessage{m}
	}
	for _, m := range chosen {
		kind, name, ok := m.Source()
		if !ok {
			return fmt.Errorf("dead letter %s carries no source attribute, so tako cannot tell which queue or topic to put it back on — inspect it with tako dlq %s and re-publish from your own code if it still matters",
				m.ID, service)
		}
		attrs := map[string]string{}
		for k, v := range m.Attributes {
			// The dead-letter stamps describe the FAILURE, not the
			// message; carrying them back would make a replayed message
			// look pre-failed to anything reading attributes.
			if k == "tako-dead-reason" || k == "tako-service" {
				continue
			}
			attrs[k] = v
		}
		topic := gcpb.TopicName(t.env, name)
		if kind == "topic" {
			// A topic fans out; this dead letter belongs to ONE
			// subscriber. The attribute addresses it, and every other
			// subscriber acks and drops (pkg/runtime/push.go).
			attrs[gcp.ReplayAttr] = service
		}
		if err := client.Republish(ctx, topic, m.Payload, attrs); err != nil {
			return err
		}
		if err := client.AckDead(ctx, sub, []string{m.AckID}); err != nil {
			return fmt.Errorf("replayed %s onto %s but could not take it off the DLQ (%v) — re-run tako dlq %s --replay to see whether it is still listed; a duplicate delivery is possible",
				m.ID, topic, err, service)
		}
		fmt.Printf("↩ replayed %s → %s (%s)\n", m.ID, name, kind)
	}
	if len(chosen) > 1 {
		fmt.Printf("✓ %d message(s) back in delivery and off the DLQ\n", len(chosen))
	}
	return nil
}

// runDLQLocalArgs parses the shared dlq flags without a cloud target and
// hands off to the on-disk implementation.
func runDLQLocalArgs(args []string) error {
	ack, replay, all := false, false, false
	var carrier, id string
	for _, a := range args {
		switch {
		case a == "--ack":
			ack = true
			continue
		case a == "--replay":
			replay = true
			continue
		case a == "--all":
			all = true
			continue
		case strings.HasPrefix(a, "--"):
			return fmt.Errorf("unknown argument %q (tako dlq --local accepts one carrier, --ack, --replay <id>, --replay --all)", a)
		}
		if carrier == "" {
			carrier = a
			continue
		}
		if replay && id == "" {
			id = a
			continue
		}
		return fmt.Errorf("unknown argument %q (tako dlq --local accepts one carrier, --ack, --replay <id>, --replay --all)", a)
	}
	if ack && replay {
		return fmt.Errorf("--ack and --replay do the opposite things to the same message — pick one")
	}
	if replay && id == "" && !all {
		return fmt.Errorf("--replay needs an id, or --all (tako dlq --local --replay <id>)")
	}
	if replay && id != "" && all {
		return fmt.Errorf("--replay takes an id or --all, not both")
	}
	return runDLQLocal(".tako/data", carrier, ack, replay, all, id)
}
