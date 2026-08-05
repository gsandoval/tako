package tako

import "testing"

// The declared domain reads back, which is the whole of FL7: an app
// building an absolute URL should not have to repeat the string the
// analyzer already reads from this literal.
func TestDeclaredDomainReadsBack(t *testing.T) {
	web := Service(ServiceConfig{Domain: "example.com"})
	if got := web.Domain(); got != "example.com" {
		t.Errorf("Domain() = %q, want %q", got, "example.com")
	}
}

// A service that declared no domain reads back empty rather than
// inventing a run.app URL or a localhost: the value is what the source
// says, in every environment, or nothing.
func TestUndeclaredDomainIsEmpty(t *testing.T) {
	for name, svc := range map[string]*ServiceHandle{
		"no config":     Service(),
		"no domain set": Service(ServiceConfig{Profile: ProfileLatency}),
	} {
		if got := svc.Domain(); got != "" {
			t.Errorf("%s: Domain() = %q, want empty", name, got)
		}
	}
}
