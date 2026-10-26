package analyzer

import (
	"fmt"
	"go/token"
)

// projectServiceClaims projects a service-scoped setting (Profile,
// Footprint, Traffic) from handlers and package-level Service resources
// onto each Service in graph.Services, diagnosing conflicting claims.
func projectServiceClaims[T any](
	graph *AppGraph,
	errs *ErrorList,
	fromHandler func(Handler) (T, bool),
	fromService func(Resource) (T, bool),
	equal func(a, b T) bool,
	rule string,
	conflictMsg func(svcName string, first T, firstBy string, second T, secondBy string) string,
	apply func(svc *Service, claim T, claimPos string),
) {
	for i := range graph.Services {
		svc := &graph.Services[i]
		var claim T
		hasClaim := false
		claimedBy, claimPos := "", ""
		for _, h := range graph.Handlers {
			if h.Service != svc.Name {
				continue
			}
			val, ok := fromHandler(h)
			if !ok {
				continue
			}
			if hasClaim && !equal(claim, val) {
				*errs = append(*errs, &Error{
					Pos:  h.Pos,
					Msg:  conflictMsg(svc.Name, claim, claimedBy, val, ShortVar(h)),
					Rule: rule,
				})
				continue
			}
			claim, hasClaim, claimedBy, claimPos = val, true, ShortVar(h), h.Pos
		}
		for _, r := range graph.Resources {
			if r.Kind != "service" || r.Package != svc.Package {
				continue
			}
			val, ok := fromService(r)
			if !ok {
				continue
			}
			if hasClaim && !equal(claim, val) {
				*errs = append(*errs, &Error{
					Pos:  r.Pos,
					Msg:  conflictMsg(svc.Name, claim, claimedBy, val, r.Var),
					Rule: rule,
				})
				continue
			}
			if isAppService(graph, *svc) {
				claim, hasClaim, claimedBy, claimPos = val, true, r.Var, r.Pos
			}
		}
		if hasClaim {
			apply(svc, claim, claimPos)
		}
	}
}

// checkReadOnlyDisks rejects any write edge targeting a disk declared
// with ReadOnly: true.
func checkReadOnlyDisks(graph *AppGraph, _ *token.FileSet, errs *ErrorList) {
	readOnlyDisks := map[string]bool{}
	for _, r := range graph.Resources {
		if r.Kind == "disk" && r.Config["readonly"] == "true" {
			readOnlyDisks[r.Name] = true
		}
	}
	if len(readOnlyDisks) == 0 {
		return
	}
	handlersByVar := map[string]*Handler{}
	for i := range graph.Handlers {
		handlersByVar[graph.Handlers[i].Var] = &graph.Handlers[i]
	}
	for _, e := range graph.Edges {
		if e.Kind == "resource" && e.ToKind == "disk" && e.Verb == "write" && readOnlyDisks[e.To] {
			pos := e.From
			if h := handlersByVar[e.From]; h != nil {
				pos = h.Pos
			}
			*errs = append(*errs, &Error{
				Pos:  pos,
				Msg:  fmt.Sprintf("handler %s writes to disk %q, which is declared with ReadOnly: true", e.From, e.To),
				Rule: "config-literal",
			})
		}
	}
}
