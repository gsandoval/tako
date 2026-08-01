package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"strconv"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
)

// ProfileKnobs maps a scaling profile to its Cloud Run values (spec
// §4.6). One table serves both sides: the provisioner derives service
// specs from it, and footprint validation resolves overrides against it
// so impossible combinations fail analysis, not deploy.
var ProfileKnobs = map[string]Footprint{
	"default": {CPU: 1, Memory: "512Mi", Concurrency: 80, MinInstances: 0, MaxInstances: 20,
		TimeoutSeconds: 300, CPUAlwaysOn: ToggleOff, DrainSeconds: DefaultDrainSeconds,
		StartupBoost: ToggleOff, SessionAffinity: ToggleOff, HTTP2: ToggleOff},
	// Latency buys tail latency three ways: a warm floor, CPU that never
	// throttles, and a boost on the cold start that a warm floor cannot
	// prevent. Leaving any of the three unset makes the profile's name a
	// claim it does not deliver.
	"latency": {CPU: 2, Memory: "1Gi", Concurrency: 40, MinInstances: 1, MaxInstances: 100,
		TimeoutSeconds: 300, CPUAlwaysOn: ToggleOn, DrainSeconds: DefaultDrainSeconds,
		StartupBoost: ToggleOn, SessionAffinity: ToggleOff, HTTP2: ToggleOff},
	"batch": {CPU: 2, Memory: "2Gi", Concurrency: 10, MinInstances: 0, MaxInstances: 10,
		TimeoutSeconds: 900, CPUAlwaysOn: ToggleOff, DrainSeconds: DefaultDrainSeconds,
		StartupBoost: ToggleOff, SessionAffinity: ToggleOff, HTTP2: ToggleOff},
	// Singleton: min=1/max=1 keeps the loop's host alive; the LEASE is
	// what makes it exactly-one (spec §4.4). Its loop runs between
	// requests, so CPU stays allocated.
	"singleton": {CPU: 1, Memory: "512Mi", Concurrency: 1, MinInstances: 1, MaxInstances: 1,
		TimeoutSeconds: 3600, CPUAlwaysOn: ToggleOn, DrainSeconds: DefaultDrainSeconds,
		StartupBoost: ToggleOff, SessionAffinity: ToggleOff, HTTP2: ToggleOff},
}

// The shutdown window is Cloud Run's, not tako's: SIGTERM, then SIGKILL
// 10 seconds later. Neither the v1 RevisionSpec nor the v2
// RevisionTemplate has a field for it and gcloud has no flag — checked
// against both discovery documents, not remembered. So a footprint
// divides the window instead of setting it.
const (
	// ShutdownWindowSeconds is what Cloud Run gives us, total.
	ShutdownWindowSeconds = 10
	// DefaultDrainSeconds is the share that goes to finishing in-flight
	// requests. The rest pays for flushing metrics and closing cleanly,
	// with a second in hand for the kill to land late.
	DefaultDrainSeconds = 7
	// MaxDrainSeconds is the most a drain may claim. Above this there is
	// no room left to flush, and at 10 the kill arrives mid-sentence.
	MaxDrainSeconds = 9
)

// Toggle states, mirroring tako.Toggle.
const (
	ToggleInherit = 0
	ToggleOn      = 1
	ToggleOff     = 2
)

// Over layers a footprint's set fields on top of base: every non-zero
// field wins, every zero field inherits.
func (f Footprint) Over(base Footprint) Footprint {
	out := base
	if f.CPU != 0 {
		out.CPU = f.CPU
	}
	if f.Memory != "" {
		out.Memory = f.Memory
	}
	if f.Concurrency != 0 {
		out.Concurrency = f.Concurrency
	}
	if f.MinInstances != 0 {
		out.MinInstances = f.MinInstances
	}
	if f.MaxInstances != 0 {
		out.MaxInstances = f.MaxInstances
	}
	if f.TimeoutSeconds != 0 {
		out.TimeoutSeconds = f.TimeoutSeconds
	}
	if f.CPUAlwaysOn != ToggleInherit {
		out.CPUAlwaysOn = f.CPUAlwaysOn
	}
	if f.DrainSeconds != 0 {
		out.DrainSeconds = f.DrainSeconds
	}
	if f.StartupBoost != ToggleInherit {
		out.StartupBoost = f.StartupBoost
	}
	if f.SessionAffinity != ToggleInherit {
		out.SessionAffinity = f.SessionAffinity
	}
	if f.HTTP2 != ToggleInherit {
		out.HTTP2 = f.HTTP2
	}
	return out
}

// String renders the SET fields for summaries and error texts,
// e.g. "memory 4Gi, concurrency 4".
func (f Footprint) String() string {
	var parts []string
	if f.CPU != 0 {
		parts = append(parts, fmt.Sprintf("cpu %d", f.CPU))
	}
	if f.Memory != "" {
		parts = append(parts, "memory "+f.Memory)
	}
	if f.Concurrency != 0 {
		parts = append(parts, fmt.Sprintf("concurrency %d", f.Concurrency))
	}
	if f.MinInstances != 0 {
		parts = append(parts, fmt.Sprintf("min %d", f.MinInstances))
	}
	if f.MaxInstances != 0 {
		parts = append(parts, fmt.Sprintf("max %d", f.MaxInstances))
	}
	if f.TimeoutSeconds != 0 {
		parts = append(parts, fmt.Sprintf("timeout %ds", f.TimeoutSeconds))
	}
	switch f.CPUAlwaysOn {
	case ToggleOn:
		parts = append(parts, "cpu always on")
	case ToggleOff:
		parts = append(parts, "cpu throttled")
	}
	if f.DrainSeconds != 0 {
		parts = append(parts, fmt.Sprintf("drain %ds", f.DrainSeconds))
	}
	switch f.StartupBoost {
	case ToggleOn:
		parts = append(parts, "startup boost")
	case ToggleOff:
		parts = append(parts, "no startup boost")
	}
	switch f.SessionAffinity {
	case ToggleOn:
		parts = append(parts, "session affinity")
	case ToggleOff:
		parts = append(parts, "no session affinity")
	}
	switch f.HTTP2 {
	case ToggleOn:
		parts = append(parts, "http/2 end to end")
	case ToggleOff:
		parts = append(parts, "http/1.1 to the instance")
	}
	return strings.Join(parts, ", ")
}

// memMi parses a Cloud Run memory limit ("512Mi", "4Gi") into MiB;
// ok is false for anything else.
func memMi(s string) (int64, bool) {
	var unit int64
	switch {
	case strings.HasSuffix(s, "Gi"):
		unit = 1024
	case strings.HasSuffix(s, "Mi"):
		unit = 1
	default:
		return 0, false
	}
	n, err := strconv.ParseInt(s[:len(s)-2], 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n * unit, true
}

// footprintArg reads WithFootprint's tako.Footprint literal. Fields must
// be constants (footprints are read at analysis time); each field is
// validated against Cloud Run's limits with a positioned error. Returns
// nil after reporting when anything is wrong.
func footprintArg(dir string, pkg *packages.Package, opt ast.Expr, errs *ErrorList) *Footprint {
	bad := false
	fail := func(pos ast.Node, msg string) {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, pos.Pos()),
			Msg:  msg,
			Rule: "footprint",
		})
		bad = true
	}
	call, ok := opt.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil
	}
	lit, ok := call.Args[0].(*ast.CompositeLit)
	if !ok {
		fail(call.Args[0], "tako.WithFootprint takes a tako.Footprint literal with constant fields — footprints are read at analysis time, not run time")
		return nil
	}
	f := &Footprint{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			fail(el, "tako.Footprint fields must be named (CPU: 2, Memory: \"1Gi\", …) — positional literals hide which knob is which")
			return nil
		}
		key := kv.Key.(*ast.Ident).Name
		tv, ok := pkg.TypesInfo.Types[kv.Value]
		if !ok || tv.Value == nil {
			fail(kv.Value, fmt.Sprintf("footprint %s must be a constant — footprints are read at analysis time, not run time", key))
			continue
		}
		switch key {
		case "Memory":
			f.Memory = constant.StringVal(tv.Value)
		default:
			n, _ := constant.Int64Val(tv.Value)
			switch key {
			case "CPU":
				f.CPU = int(n)
			case "Concurrency":
				f.Concurrency = int(n)
			case "MinInstances":
				f.MinInstances = int(n)
			case "MaxInstances":
				f.MaxInstances = int(n)
			case "Timeout":
				// time.Duration is an int64 of NANOSECONDS; Cloud Run
				// speaks whole seconds, so a sub-second timeout is a
				// mistake worth naming rather than truncating to zero.
				if n > 0 && n < int64(time.Second) {
					fail(kv.Value, fmt.Sprintf("footprint Timeout must be at least one second; got %s", time.Duration(n)))
				}
				f.TimeoutSeconds = int(n / int64(time.Second))
			case "CPUAlwaysOn":
				f.CPUAlwaysOn = int(n)
			case "Drain":
				// Nanoseconds, like Timeout: a sub-second drain is a unit
				// mistake, not a request to shut down instantly.
				if n > 0 && n < int64(time.Second) {
					fail(kv.Value, fmt.Sprintf("footprint Drain must be at least one second; got %s", time.Duration(n)))
				}
				f.DrainSeconds = int(n / int64(time.Second))
			case "StartupBoost":
				f.StartupBoost = int(n)
			case "SessionAffinity":
				f.SessionAffinity = int(n)
			case "HTTP2":
				f.HTTP2 = int(n)
			}
		}
	}
	// The CEILING depends on the deployable — a service's request timeout
	// caps at 60m, a job task's at 24h — and this parser cannot know
	// which it is looking at. It rejects what is impossible everywhere;
	// applyFootprints rejects what is impossible for THIS service.
	if f.TimeoutSeconds < 0 || f.TimeoutSeconds > 24*3600 {
		fail(lit, fmt.Sprintf("footprint Timeout must be between 1s and 24h; got %s", time.Duration(f.TimeoutSeconds)*time.Second))
	}
	if f.CPUAlwaysOn < 0 || f.CPUAlwaysOn > 2 {
		fail(lit, "footprint CPUAlwaysOn must be tako.Inherit, tako.On, or tako.Off")
	}
	if f.StartupBoost < 0 || f.StartupBoost > 2 {
		fail(lit, "footprint StartupBoost must be tako.Inherit, tako.On, or tako.Off")
	}
	if f.SessionAffinity < 0 || f.SessionAffinity > 2 {
		fail(lit, "footprint SessionAffinity must be tako.Inherit, tako.On, or tako.Off")
	}
	if f.HTTP2 < 0 || f.HTTP2 > 2 {
		fail(lit, "footprint HTTP2 must be tako.Inherit, tako.On, or tako.Off")
	}
	// The shutdown window is Cloud Run's and it is fixed at 10s. Clamping
	// a bigger number would leave the declaration reading like a promise
	// tako keeps, so refuse it instead and say who set the ceiling.
	if f.DrainSeconds < 0 || f.DrainSeconds > MaxDrainSeconds {
		fail(lit, fmt.Sprintf("footprint Drain must be between 1s and %ds; got %s — Cloud Run kills the instance %ds after SIGTERM and that window is not configurable, so a longer drain is a promise tako cannot keep",
			MaxDrainSeconds, time.Duration(f.DrainSeconds)*time.Second, ShutdownWindowSeconds))
	}
	switch f.CPU {
	case 0, 1, 2, 4, 8:
	default:
		fail(lit, fmt.Sprintf("footprint CPU must be 1, 2, 4, or 8 vCPUs; got %d", f.CPU))
	}
	if f.Memory != "" {
		if mi, ok := memMi(f.Memory); !ok || mi < 128 || mi > 32*1024 {
			fail(lit, fmt.Sprintf("footprint Memory must be like \"512Mi\" or \"4Gi\", between 128Mi and 32Gi; got %q", f.Memory))
		}
	}
	if f.Concurrency < 0 || f.Concurrency > 1000 {
		fail(lit, fmt.Sprintf("footprint Concurrency must be between 1 and 1000; got %d", f.Concurrency))
	}
	if f.MinInstances < 0 || f.MinInstances > 1000 {
		fail(lit, fmt.Sprintf("footprint MinInstances must be between 0 and 1000; got %d", f.MinInstances))
	}
	if f.MaxInstances < 0 || f.MaxInstances > 1000 {
		fail(lit, fmt.Sprintf("footprint MaxInstances must be between 1 and 1000; got %d", f.MaxInstances))
	}
	if f.MinInstances > 0 && f.MaxInstances > 0 && f.MinInstances > f.MaxInstances {
		fail(lit, fmt.Sprintf("footprint MinInstances %d exceeds MaxInstances %d", f.MinInstances, f.MaxInstances))
	}
	if bad {
		return nil
	}
	if (*f == Footprint{}) {
		fail(lit, "footprint sets nothing — drop tako.WithFootprint or pin at least one field")
		return nil
	}
	return f
}

// applyFootprints projects handler WithFootprint claims onto services,
// rejects conflicting claims (one service, one footprint), and
// validates each service's RESOLVED knobs — profile base plus overrides
// — against Cloud Run's cross-field rules, so a footprint that only
// pins CPU cannot silently pair it with too little inherited memory.
func applyFootprints(graph *AppGraph, errs *ErrorList) {
	for i := range graph.Services {
		svc := &graph.Services[i]
		var claim *Footprint
		claimedBy, claimPos := "", ""
		for _, h := range graph.Handlers {
			if h.Service != svc.Name || h.Footprint == nil {
				continue
			}
			if claim != nil && *claim != *h.Footprint {
				*errs = append(*errs, &Error{
					Pos:  h.Pos,
					Msg:  fmt.Sprintf("service %s cannot carry both footprint (%s) from %s and footprint (%s) from %s — one service scales one way; split the packages or align the footprints", svc.Name, claim, claimedBy, h.Footprint, ShortVar(h)),
					Rule: "mixed-footprint",
				})
				continue
			}
			claim, claimedBy, claimPos = h.Footprint, ShortVar(h), h.Pos
		}
		for _, r := range graph.Resources {
			if r.Kind != "service" || r.Package != svc.Package || r.Footprint == nil {
				continue
			}
			if claim != nil && *claim != *r.Footprint {
				*errs = append(*errs, &Error{
					Pos:  r.Pos,
					Msg:  fmt.Sprintf("service %s cannot carry both footprint (%s) from %s and footprint (%s) from %s — one service scales one way; split the packages or align the footprints", svc.Name, claim, claimedBy, r.Footprint, r.Var),
					Rule: "mixed-footprint",
				})
				continue
			}
			if isAppService(graph, *svc) {
				claim, claimedBy, claimPos = r.Footprint, r.Var, r.Pos
			}
		}
		if claim == nil {
			continue
		}
		svc.Footprint = claim

		r := claim.Over(ProfileKnobs[svc.Profile])
		mi, _ := memMi(r.Memory)
		cpus := fmt.Sprintf("%d vCPU", r.CPU)
		if r.CPU != 1 {
			cpus += "s"
		}
		resolveFail := func(msg string) {
			*errs = append(*errs, &Error{Pos: claimPos, Msg: msg, Rule: "footprint"})
		}
		switch {
		case r.CPU >= 8 && mi < 4*1024:
			resolveFail(fmt.Sprintf("service %s resolves to %s with %s memory — 8 vCPUs need at least 4Gi; pin Memory too", svc.Name, cpus, r.Memory))
		case r.CPU >= 4 && mi < 2*1024:
			resolveFail(fmt.Sprintf("service %s resolves to %s with %s memory — 4 vCPUs need at least 2Gi; pin Memory too", svc.Name, cpus, r.Memory))
		}
		switch {
		case mi > 16*1024 && r.CPU < 8:
			resolveFail(fmt.Sprintf("service %s resolves to %s memory with %s — over 16Gi needs 8 vCPUs; pin CPU too", svc.Name, r.Memory, cpus))
		case mi > 8*1024 && r.CPU < 4:
			resolveFail(fmt.Sprintf("service %s resolves to %s memory with %s — over 8Gi needs at least 4 vCPUs; pin CPU too", svc.Name, r.Memory, cpus))
		case mi > 4*1024 && r.CPU < 2:
			resolveFail(fmt.Sprintf("service %s resolves to %s memory with %s — over 4Gi needs at least 2 vCPUs; pin CPU too", svc.Name, r.Memory, cpus))
		}
		// Cloud Run's two ceilings: a request may run 60 minutes, a job
		// task 24 hours. Resolved, because a profile can supply it.
		if !svc.Job && r.TimeoutSeconds > 3600 {
			resolveFail(fmt.Sprintf("service %s resolves to a %s request timeout — Cloud Run caps a request at 60m; for work that outlives a request use a tako.Job (docs/reference/job.md)",
				svc.Name, time.Duration(r.TimeoutSeconds)*time.Second))
		}
		if r.MinInstances > r.MaxInstances {
			resolveFail(fmt.Sprintf("service %s resolves to MinInstances %d over MaxInstances %d (profile %s) — pin MaxInstances too", svc.Name, r.MinInstances, r.MaxInstances, svc.Profile))
		}
	}
}
