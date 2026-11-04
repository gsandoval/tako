package entityttl

import "github.com/gsandoval/tako"

var sessions = tako.Entity[Session]("sessions")

type Session struct {
	ID      string `entity:"id"`
	Expires int64  `entity:"ttl"`
}

var Get = tako.API("GET /api/sessions", getSession)

func getSession(ctx tako.Ctx, _ struct{}) (Session, error) {
	return sessions.Get(ctx, "s1")
}
