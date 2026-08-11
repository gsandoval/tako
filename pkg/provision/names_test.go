package provision

import (
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

func bucketGraph(names ...string) *analyzer.AppGraph {
	g := &analyzer.AppGraph{}
	for _, n := range names {
		g.Resources = append(g.Resources, analyzer.Resource{Kind: "bucket", Name: n})
	}
	return g
}

func TestRealisticBucketNamesPass(t *testing.T) {
	cfg := Config{Project: "tako-test-02", App: "github.com/gsandoval/flauta", Env: "staging"}
	if err := CheckNames(bucketGraph("originals", "thumbs"), cfg); err != nil {
		t.Errorf("CheckNames = %v, want nil", err)
	}
}

// The check exists because a derived id lengthens the middle of a name
// whose ends the user chose. It has to name all four parts, because
// any of them can be the one to shorten.
func TestAnOverlongBucketNameIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	cfg := Config{
		Project: "a-rather-long-project-id-here",
		App:     "github.com/an-organisation/a-long-service-name",
		Env:     "integration-test",
	}
	err := CheckNames(bucketGraph("user-uploaded-originals"), cfg)
	if err == nil {
		t.Fatal("CheckNames accepted a name past 63 characters")
	}
	got := err.Error()
	for _, want := range []string{
		`bucket "user-uploaded-originals" would be created as`,
		"characters)",
		"Google caps bucket names at 63",
		"<project>-<app id>-<env>-<declared name>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q:\n%s", want, got)
		}
	}
	// The assembled name is in the message, because "too long" without
	// it leaves the reader to assemble it by hand.
	if !strings.Contains(got, cfg.Project+"-"+cfg.AppID()+"-"+cfg.Env+"-user-uploaded-originals") {
		t.Errorf("message does not show the assembled name:\n%s", got)
	}
}

// prod deploys bare names, so the same declaration can be legal in
// prod and illegal in a long-named env — the check has to run against
// the env being deployed, not against prod.
func TestTheCheckFollowsTheEnv(t *testing.T) {
	g := bucketGraph("user-photographs")
	cfg := Config{Project: "a-rather-long-project-id-here", App: "github.com/org/service", Env: "prod"}
	if err := CheckNames(g, cfg); err != nil {
		t.Errorf("prod: %v", err)
	}
	cfg.Env = "integration-test"
	if err := CheckNames(g, cfg); err == nil {
		t.Error("integration-test: accepted a name prod only just fits")
	}
}

// Every example must pass with its own module path: this check runs on
// every deploy, and a false positive would block one.
func TestExampleShapedNamesPass(t *testing.T) {
	for _, app := range []string{"gallery", "hello", "github.com/gsandoval/tako-example-hello"} {
		cfg := Config{Project: "tako-test-01", App: app, Env: "prod"}
		if err := CheckNames(bucketGraph("photos", "thumbs"), cfg); err != nil {
			t.Errorf("app %q: %v", app, err)
		}
	}
}

// CR7: env isolation is a string prefix with no escaping, so it is not
// injective. Two declarations can name the same physical resource in
// some environment, and the environment where they collide is the one
// nobody deployed yet.
func TestTwoNamesThatBecomeOnePhysicalNameAreRefused(t *testing.T) {
	g := &analyzer.AppGraph{Resources: []analyzer.Resource{
		{Kind: "topic", Name: "orders"},
		{Kind: "topic", Name: "staging-orders"},
	}}
	err := CheckNames(g, Config{Project: "p", App: "shop", Env: "prod"})
	if err == nil {
		t.Fatal(`"orders" in env staging and "staging-orders" in prod are one Pub/Sub topic; ` +
			"the deploy accepted both")
	}
	for _, want := range []string{"orders", "staging-orders", "staging"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q:\n%s", want, err)
		}
	}
}

// The derived names collide the same way: a bucket's event topic is
// <bucket>-uploaded, so a queue spelled that way IS that topic.
func TestADerivedNameCannotBeDeclaredByHand(t *testing.T) {
	g := &analyzer.AppGraph{Resources: []analyzer.Resource{
		{Kind: "bucket", Name: "photos"},
		{Kind: "queue", Name: "photos-uploaded"},
	}}
	if err := CheckNames(g, Config{Project: "p", App: "gallery", Env: "prod"}); err == nil {
		t.Fatal("a queue named photos-uploaded is the bucket photos' own event topic")
	}
}

// And the ordinary case still passes: a shared prefix is not a
// collision unless the remainder is itself declared.
func TestASharedPrefixIsNotACollision(t *testing.T) {
	g := &analyzer.AppGraph{Resources: []analyzer.Resource{
		{Kind: "topic", Name: "order-placed"},
		{Kind: "topic", Name: "order-shipped"},
		{Kind: "queue", Name: "staging-only-queue"},
	}}
	if err := CheckNames(g, Config{Project: "p", App: "shop", Env: "prod"}); err != nil {
		t.Errorf("false positive on ordinary names: %v", err)
	}
}
