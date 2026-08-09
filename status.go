package tako

// Status pages: the answers tako gives on your behalf.
//
// Most responses come from a handler you wrote. Some do not — a URL
// that matches no route, a form whose CSRF token expired, a request
// that needs a sign-in, a handler that returned an error. tako answers
// those itself, and before this existed every one of them was a dead
// end that did not look like your app: plain text, or a 404 template
// rendered with no data, so a shell built from config rendered its
// bands empty.
//
// Those were filed as separate gaps, days apart. They are one: the app
// had no way to say what a page tako owns should look like. StatusPage
// is that seam, and it covers the statuses tako learns to answer later
// without anyone filing anything.

// Status is what tako can honestly tell a status page about the
// response it is producing.
//
// There is deliberately no error field. A 4xx carries a Message the
// visitor may read; a 5xx carries none, because the failure is logged
// against RequestID and putting it here would make leaking it the easy
// thing to do. The type is the guard.
type Status struct {
	// Code is the HTTP status being answered: 404, 403, 405, 500.
	Code int
	// Path is the request path that produced it.
	Path string
	// RequestID ties this page to the log line that explains it. It is
	// the one thing worth showing a visitor on a 500.
	RequestID string
	// Message is safe, already-public text — "CSRF token missing or
	// invalid" — and is empty for any 5xx.
	Message string
	// Policy names the declared policy that refused this request —
	// "Editors" — and is empty for every status tako answers on its own
	// account, CSRF included.
	//
	// It exists because a 403 from your rule and a 403 from a stale form
	// are different problems with different remediations, and a status
	// page that cannot tell them apart sends a signed-in editor who
	// tripped CSRF to ask for access they already have. The first app to
	// meet this sniffed a sentinel string out of Message to decide; this
	// is that, typed.
	Policy string
}

// StatusHandle is a declared status page, the value tako.StatusPage
// returns. Assign it to an exported package-level var like any other
// handler; the generated service registers it.
type StatusHandle struct {
	pkg string
	fn  func(Ctx, Status) (HTML, error)
}

// StatusPage declares the page tako renders for the responses it
// produces itself — 404, 403, 405, 500, and whatever it learns to
// answer next. One per package.
//
//	var Status = tako.StatusPage(status)
//
//	func status(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
//		return site.Render(ctx, "status.html", StatusView{
//			Code: s.Code, Nav: nav(), RequestID: s.RequestID,
//		})
//	}
//
// It is called with the same Ctx a page handler gets, so it can read
// the session, log, and query — a status page is an ordinary page that
// tako decides when to show.
//
// A status page can never make things worse: if it panics, errors, or
// renders nothing, tako falls back to plain text with the same status
// code. The visitor already could not get what they wanted, and an
// error page that errors helps nobody.
func StatusPage(fn func(Ctx, Status) (HTML, error)) StatusHandle {
	return StatusHandle{pkg: pkgOf(fn), fn: fn}
}

// Render invokes the declared status page. The runtime is the caller;
// user code never needs this.
func (h StatusHandle) Render(ctx Ctx, s Status) (HTML, error) {
	if h.fn == nil {
		return HTML{}, nil
	}
	return h.fn(ctx, s)
}

// Declared reports whether this handle carries a page.
func (h StatusHandle) Declared() bool { return h.fn != nil }
