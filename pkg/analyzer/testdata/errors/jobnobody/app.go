package jobnobody

import "github.com/gsandoval/tako"

// A declared job with no Runner: the execution would be provisioned and
// nothing would answer it. This is a whole-graph judgment, so it is
// reached only once every declaration reads cleanly — which is why it
// has a fixture of its own rather than sharing jobbinding's.
var reindex = tako.Job[Params]("reindex")

type Params struct {
	Since string `json:"since"`
}

var _ = reindex
