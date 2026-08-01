package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/backends"
	"github.com/gsandoval/tako/pkg/backends/local"
)

// localDead is one dead letter on disk, with the id the CLI addresses
// it by. Local dead letters are files under
// .tako/data/deadletter/<queue>/ or .../<topic>/<subscriber>/, so the
// file's stem is a stable id for free — the local counterpart of
// Pub/Sub's messageId.
type localDead struct {
	ID     string
	Path   string
	Source string // queue or topic
	Sub    string // subscriber, empty for queues
	Record backends.DeadLetter
}

// localName is how a dead letter's carrier reads in the listing:
// "thumbnails" for a queue, "photo-ready/NotifyOwner" for a topic.
func (d localDead) localName() string {
	if d.Sub == "" {
		return d.Source
	}
	return d.Source + "/" + d.Sub
}

// readLocalDead walks the on-disk dead-letter tree. The replay inbox is
// skipped: a message in there has already left the DLQ and is waiting
// for the dev server to pick it up.
func readLocalDead(dir string) ([]localDead, error) {
	root := filepath.Join(dir, "deadletter")
	var out []localDead
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if parts[0] == local.ReplayDir {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var rec backends.DeadLetter
		if json.Unmarshal(raw, &rec) != nil {
			return nil
		}
		d := localDead{
			ID:     strings.TrimSuffix(e.Name(), ".json"),
			Path:   path,
			Source: rec.Queue,
			Sub:    rec.Subscriber,
			Record: rec,
		}
		out = append(out, d)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// runDLQLocal is `tako dlq --local [carrier] [--ack|--replay …]`, the
// on-disk counterpart of the cloud path. It exists so the two backends
// answer the same command with the same semantics — replay is a MOVE
// either way, and a replayed message that fails again comes back.
func runDLQLocal(dataDir, carrier string, ack, replay, all bool, prefix string) error {
	msgs, err := readLocalDead(dataDir)
	if err != nil {
		return err
	}
	if carrier != "" {
		var kept []localDead
		for _, m := range msgs {
			if m.Source == carrier || m.localName() == carrier || m.Sub == carrier {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			var names []string
			seen := map[string]bool{}
			for _, m := range msgs {
				if !seen[m.localName()] {
					seen[m.localName()] = true
					names = append(names, m.localName())
				}
			}
			if len(names) == 0 {
				return fmt.Errorf("no dead letters under %s — nothing has failed past its attempts", filepath.Join(dataDir, "deadletter"))
			}
			return fmt.Errorf("no dead letters for %q — carriers with dead letters: %s", carrier, strings.Join(names, ", "))
		}
		msgs = kept
	}
	if len(msgs) == 0 {
		fmt.Printf("no dead letters under %s\n", filepath.Join(dataDir, "deadletter"))
		return nil
	}

	if replay {
		chosen := msgs
		if !all {
			m, err := pickLocalDead(msgs, prefix)
			if err != nil {
				return err
			}
			chosen = []localDead{m}
		}
		inbox := filepath.Join(dataDir, "deadletter", local.ReplayDir)
		if err := os.MkdirAll(inbox, 0o755); err != nil {
			return err
		}
		for _, m := range chosen {
			// A rename, not a copy: the message leaves the DLQ in the same
			// instant it enters delivery, with no window where it is in
			// both places or neither.
			if err := os.Rename(m.Path, filepath.Join(inbox, m.ID+".json")); err != nil {
				return fmt.Errorf("replay %s: %v", m.ID, err)
			}
			fmt.Printf("↩ replayed %s → %s\n", m.ID, m.localName())
		}
		fmt.Println("  the running tako dev picks these up within a couple of seconds; if dev is down they wait for its next boot")
		return nil
	}

	for _, m := range msgs {
		reason := m.Record.LastError
		if m.Record.Permanent {
			reason = "permanent: " + reason
		}
		fmt.Printf("  %s  %-28s attempts %d  %s\n    %s\n",
			m.ID, m.localName(), m.Record.Attempts, reason, m.Record.Payload)
	}
	if ack {
		for _, m := range msgs {
			if err := os.Remove(m.Path); err != nil {
				return err
			}
		}
		fmt.Printf("✓ acknowledged %d message(s) — deleted from disk\n", len(msgs))
	}
	return nil
}

// pickLocalDead resolves an id prefix, matching the cloud path's
// git-style short ids.
func pickLocalDead(msgs []localDead, prefix string) (localDead, error) {
	var hits []localDead
	for _, m := range msgs {
		if strings.HasPrefix(m.ID, prefix) {
			hits = append(hits, m)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return localDead{}, fmt.Errorf("no dead letter matching %q — tako dlq --local lists what is there", prefix)
	default:
		return localDead{}, fmt.Errorf("%q matches %d dead letters — use more characters", prefix, len(hits))
	}
}
