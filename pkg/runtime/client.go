package runtime

// Who sent the request (AB3a).
//
// The app that asked for this needed a per-sender rate limit on the one
// unauthenticated write path it had, and could not express one: nothing
// in tako.Ctx knew anything about the caller. So it capped submissions
// SITE-WIDE at 30/hour instead, and wrote down what that costs — one
// determined sender locks the form for everybody.
//
// The whole feature is two fields. What makes it worth care is the
// first one.

import (
	"net"
	"net/http"
	"strings"

	tako "github.com/gsandoval/tako"
)

// trustedHops is how many proxies tako itself puts in front of a
// service: exactly one, Google's frontend. A custom load balancer would
// add another, and tako does not provision one (docs/domain-options.md).
const trustedHops = 1

// clientOf reads the caller off a request.
//
// X-Forwarded-For is a list the CLIENT can start: anything it sends
// arrives as the left-most entries, and Google's frontend appends to it
// rather than replacing it. So the left-most entry is not the caller —
// it is whatever the caller wanted the caller to look like, and a rate
// limit keyed on it is a rate limit with an opt-out.
//
// The honest read is from the RIGHT, past the hops we put there: the
// last entry is Google's frontend, and the one before it is the address
// the frontend saw. Everything to the left of that is unverifiable and
// is ignored.
//
// With no proxy in front at all — `tako dev`, a test, a bare binary —
// there is no list to read and the answer is the socket.
func clientOf(r *http.Request) tako.Client {
	return tako.Client{IP: clientIP(r), UserAgent: r.Header.Get("User-Agent")}
}

func clientIP(r *http.Request) string {
	var hops []string
	for _, raw := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(part); v != "" {
				hops = append(hops, v)
			}
		}
	}
	// A list SHORTER than the hops tako expects did not come through
	// them: it is a header somebody set, and it says nothing. Fall back
	// to the socket, which cannot be forged over TCP.
	if len(hops) > trustedHops {
		return hops[len(hops)-1-trustedHops]
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
