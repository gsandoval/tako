package analyzer

// PrimitiveSpec is the single source of truth for one SDK constructor's
// static-analysis properties across the Go and CFire frontends.
type PrimitiveSpec struct {
	Ctor            string
	Kind            string
	IsHandler       bool
	RejectLocalDecl bool
	PhysicalName    bool
	MermaidShape    string // "cylinder", "queue", "hexagon", "box", "stadium"
	MethodVerbs     map[string]string
}

// Registry maps each SDK constructor name ("API", "Entity", "Disk", ...)
// to its PrimitiveSpec.
var Registry = map[string]PrimitiveSpec{
	// Handlers
	"API":       {Ctor: "API", Kind: "api", IsHandler: true, RejectLocalDecl: true, MermaidShape: "box"},
	"Page":      {Ctor: "Page", Kind: "page", IsHandler: true, RejectLocalDecl: true, MermaidShape: "box"},
	"Worker":    {Ctor: "Worker", Kind: "worker", IsHandler: true, RejectLocalDecl: true, MermaidShape: "box"},
	"Subscribe": {Ctor: "Subscribe", Kind: "subscribe", IsHandler: true, RejectLocalDecl: true, MermaidShape: "box"},
	"Cron":      {Ctor: "Cron", Kind: "cron", IsHandler: true, RejectLocalDecl: true, MermaidShape: "box"},
	"Runner":    {Ctor: "Runner", Kind: "job", IsHandler: true, RejectLocalDecl: true, MermaidShape: "box"},
	"Singleton": {Ctor: "Singleton", Kind: "singleton", IsHandler: true, RejectLocalDecl: true, MermaidShape: "box"},

	// Resources & Declarations
	"Entity": {
		Ctor: "Entity", Kind: "entity", RejectLocalDecl: true, MermaidShape: "cylinder",
		MethodVerbs: map[string]string{
			"Get": "read", "Exists": "read", "Query": "read",
			"Put": "write", "Delete": "write", "Update": "write", "Tx": "write",
		},
	},
	"Bucket": {
		Ctor: "Bucket", Kind: "bucket", RejectLocalDecl: true, PhysicalName: true, MermaidShape: "cylinder",
		MethodVerbs: map[string]string{
			"Get": "read", "Exists": "read", "List": "read",
			"Put": "write", "Delete": "write", "Take": "write",
			"SignedPut": "sign", "SignedGet": "sign",
		},
	},
	"Disk": {
		Ctor: "Disk", Kind: "disk", RejectLocalDecl: true, PhysicalName: true, MermaidShape: "cylinder",
		MethodVerbs: map[string]string{
			"ReadFile": "read", "Open": "read", "Stat": "read", "FS": "read", "Path": "read",
			"WriteFile": "write", "Create": "write", "OpenFile": "write", "Remove": "write", "MkdirAll": "write",
		},
	},
	"Queue": {
		Ctor: "Queue", Kind: "queue", RejectLocalDecl: true, PhysicalName: true, MermaidShape: "hexagon",
		MethodVerbs: map[string]string{
			"Publish": "publish", "PublishAfter": "publish",
		},
	},
	"Topic": {
		Ctor: "Topic", Kind: "topic", RejectLocalDecl: true, PhysicalName: true, MermaidShape: "hexagon",
		MethodVerbs: map[string]string{
			"Publish": "publish", "PublishAfter": "publish",
		},
	},
	"Cache": {
		Ctor: "Cache", Kind: "cache", RejectLocalDecl: true, MermaidShape: "cylinder",
	},
	"Secret": {
		Ctor: "Secret", Kind: "secret", RejectLocalDecl: true, MermaidShape: "stadium",
		MethodVerbs: map[string]string{
			"Value": "read", "*": "read",
		},
	},
	"Counter": {
		Ctor: "Counter", Kind: "counter", RejectLocalDecl: true, MermaidShape: "stadium",
	},
	"Histogram": {
		Ctor: "Histogram", Kind: "histogram", RejectLocalDecl: true, MermaidShape: "stadium",
	},
	"Site": {
		Ctor: "Site", Kind: "site", RejectLocalDecl: true, MermaidShape: "stadium",
	},
	"Service": {
		Ctor: "Service", Kind: "service", RejectLocalDecl: true, MermaidShape: "stadium",
	},
	"Auth": {
		Ctor: "Auth", Kind: "auth", RejectLocalDecl: true, MermaidShape: "stadium",
		MethodVerbs: map[string]string{
			"Get": "read", "ByEmail": "read", "List": "read", "Page": "read", "IsInviteOnly": "read",
			"Create": "write", "Grant": "write", "Revoke": "write", "SetInviteOnly": "write",
			"Disable": "write", "Enable": "write", "Delete": "write",
		},
	},
	"Policy":       {Ctor: "Policy", Kind: "policy", RejectLocalDecl: true},
	"RequireRole":  {Ctor: "RequireRole", Kind: "policy", RejectLocalDecl: true},
	"Role":         {Ctor: "Role", Kind: "role", RejectLocalDecl: true},
	"StatusPage":   {Ctor: "StatusPage", Kind: "status", RejectLocalDecl: true, MermaidShape: "stadium"},
	"Job":          {Ctor: "Job", Kind: "job", RejectLocalDecl: true, MermaidShape: "stadium", MethodVerbs: map[string]string{"Run": "run"}},
	"Lock":         {Ctor: "Lock", Kind: "lock", RejectLocalDecl: true, MermaidShape: "stadium", MethodVerbs: map[string]string{"Do": "lease", "TryDo": "lease"}},
	"Alert":        {Ctor: "Alert", Kind: "alert", RejectLocalDecl: true, MermaidShape: "stadium"},
	"AlertEmail":   {Ctor: "AlertEmail", Kind: "channel", RejectLocalDecl: true, MermaidShape: "stadium"},
	"AlertTopic":   {Ctor: "AlertTopic", Kind: "channel", RejectLocalDecl: true, MermaidShape: "stadium"},
	"AlertWebhook": {Ctor: "AlertWebhook", Kind: "channel", RejectLocalDecl: true, MermaidShape: "stadium"},
	"Flag": {
		Ctor: "Flag", Kind: "flag", RejectLocalDecl: true, MermaidShape: "stadium",
		MethodVerbs: map[string]string{
			"Get": "read", "GetFor": "read",
		},
	},
	"Audit": {
		Ctor: "Audit", Kind: "audit", RejectLocalDecl: true, MermaidShape: "stadium",
		MethodVerbs: map[string]string{
			"Record": "write",
		},
	},
	"Push": {
		Ctor: "Push", Kind: "push", RejectLocalDecl: true, MermaidShape: "stadium",
		MethodVerbs: map[string]string{
			"Send": "write",
		},
	},
	"Docs": {
		Ctor: "Docs", Kind: "docs", RejectLocalDecl: true, PhysicalName: true, MermaidShape: "cylinder",
		MethodVerbs: map[string]string{
			"Serve": "read", "Render": "read", "LLMsTxt": "read",
		},
	},
}

// ByKind indexes Registry by resource/handler kind. Resource specs take
// precedence when a handler kind shares a name (e.g. "job").
var ByKind = func() map[string]PrimitiveSpec {
	m := make(map[string]PrimitiveSpec, len(Registry))
	for _, spec := range Registry {
		if !spec.IsHandler {
			m[spec.Kind] = spec
		}
	}
	for _, spec := range Registry {
		if _, exists := m[spec.Kind]; !exists {
			m[spec.Kind] = spec
		}
	}
	return m
}()
