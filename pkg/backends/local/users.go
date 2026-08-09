package local

// The local accounts store: what Identity Platform is, on a laptop.
//
// It holds what the cloud holds — an id, an address, a display name,
// roles, a disabled flag — plus the one thing Identity Platform keeps to
// itself and a local sign-in cannot do without: a password verifier.
//
// The verifier is PBKDF2-HMAC-SHA256 with a random salt, from the
// standard library. Not bcrypt or argon2, which would be a dependency;
// not a bare hash, which would be a password file. It protects a
// developer's laptop fixture, and it is written to be honest about that
// rather than to pretend it is a production credential store — the
// deployed app never reaches this code, because in the cloud Identity
// Platform holds the secret.

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/gsandoval/tako/pkg/backends"
)

// usersBucket is the bbolt bucket accounts live in. The leading
// underscore keeps it out of the way of app entity kinds, the way
// _tako_session already does.
const usersBucket = "_tako_users"

// pbkdf2Iterations is deliberately modest: this guards a local fixture,
// and a slow sign-in in the dev loop is a cost paid on every reload.
const pbkdf2Iterations = 60_000

// UserStore implements the accounts contract on the same bbolt file the
// entity store uses, so `tako dev` keeps one database.
type UserStore struct{ entities *EntityStore }

// NewUserStore wraps an open entity store.
func NewUserStore(e *EntityStore) *UserStore { return &UserStore{entities: e} }

// userDoc is the stored shape. Password material lives here and nowhere
// else — it never crosses the backends.UserRecord boundary.
type userDoc struct {
	ID            string   `json:"id"`
	Email         string   `json:"email"`
	Name          string   `json:"name"`
	Roles         []string `json:"roles,omitempty"`
	Disabled      bool     `json:"disabled,omitempty"`
	EmailVerified bool     `json:"emailVerified,omitempty"`
	Salt          string   `json:"salt,omitempty"`
	Hash          string   `json:"hash,omitempty"`
}

func (d userDoc) record() backends.UserRecord {
	return backends.UserRecord{
		ID: d.ID, Email: d.Email, Name: d.Name, Roles: d.Roles,
		Disabled: d.Disabled, EmailVerified: d.EmailVerified,
	}
}

// normalizeEmail is how two addresses are judged the same. Identity
// Platform lower-cases, so this does too — otherwise Create would refuse
// a duplicate in the cloud and accept it locally, which is exactly the
// kind of difference the conformance suite exists to catch.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *UserStore) Create(ctx context.Context, spec backends.NewUserSpec) (backends.UserRecord, error) {
	email := normalizeEmail(spec.Email)
	if _, err := s.ByEmail(ctx, email); err == nil {
		return backends.UserRecord{}, backends.ErrEmailTaken
	} else if err != backends.ErrNotFound {
		return backends.UserRecord{}, err
	}
	doc := userDoc{
		ID:    "u_" + randomHex(8),
		Email: email,
		Name:  spec.Name,
		Roles: spec.Roles,
	}
	if spec.Password != "" {
		salt := randomHex(16)
		doc.Salt = salt
		hash, err := passwordHash(spec.Password, salt)
		if err != nil {
			return backends.UserRecord{}, err
		}
		doc.Hash = hash
	}
	if err := s.put(doc); err != nil {
		return backends.UserRecord{}, err
	}
	return doc.record(), nil
}

func (s *UserStore) Get(ctx context.Context, id string) (backends.UserRecord, error) {
	doc, err := s.doc(id)
	if err != nil {
		return backends.UserRecord{}, err
	}
	return doc.record(), nil
}

func (s *UserStore) ByEmail(ctx context.Context, email string) (backends.UserRecord, error) {
	want := normalizeEmail(email)
	var found *userDoc
	err := s.each(func(d userDoc) bool {
		if d.Email == want {
			copy := d
			found = &copy
			return false
		}
		return true
	})
	if err != nil {
		return backends.UserRecord{}, err
	}
	if found == nil {
		return backends.UserRecord{}, backends.ErrNotFound
	}
	return found.record(), nil
}

// List pages by id, which is stable and is also the order bbolt already
// stores in — the same keyset property Entity.Page promises.
func (s *UserStore) List(ctx context.Context, limit int, after string) ([]backends.UserRecord, string, error) {
	if limit <= 0 {
		limit = backends.UsersLimitDefault
	}
	var docs []userDoc
	err := s.each(func(d userDoc) bool {
		if after == "" || d.ID > after {
			docs = append(docs, d)
		}
		return true
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	next := ""
	if len(docs) > limit {
		docs = docs[:limit]
		next = docs[len(docs)-1].ID
	}
	out := make([]backends.UserRecord, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.record())
	}
	return out, next, nil
}

func (s *UserStore) SetRoles(ctx context.Context, id string, roles []string) error {
	if err := backends.CheckRolesSize(roles); err != nil {
		return err
	}
	return s.mutate(id, func(d *userDoc) { d.Roles = roles })
}

func (s *UserStore) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return s.mutate(id, func(d *userDoc) { d.Disabled = disabled })
}

func (s *UserStore) Delete(ctx context.Context, id string) error {
	db, err := s.entities.handle()
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(usersBucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(id))
	})
}

// VerifyPassword reports whether the address and password name a live
// account, and returns it. It is NOT part of the backends.Users
// contract: in the cloud Identity Platform verifies a password and hands
// back a token, and tako never sees one. This is the local half of that,
// used by the dev session endpoint (AB10c).
func (s *UserStore) VerifyPassword(ctx context.Context, email, password string) (backends.UserRecord, error) {
	rec, err := s.ByEmail(ctx, email)
	if err != nil {
		return backends.UserRecord{}, err
	}
	doc, err := s.doc(rec.ID)
	if err != nil {
		return backends.UserRecord{}, err
	}
	if doc.Hash == "" {
		return backends.UserRecord{}, backends.ErrNotFound
	}
	got, err := passwordHash(password, doc.Salt)
	if err != nil {
		return backends.UserRecord{}, err
	}
	// Constant time, because the comparison is the whole check.
	if subtle.ConstantTimeCompare([]byte(got), []byte(doc.Hash)) != 1 {
		return backends.UserRecord{}, backends.ErrNotFound
	}
	return doc.record(), nil
}

// SetPassword replaces the local verifier. Cloud-side this is Identity
// Platform's accounts:update; there is no shared contract for it because
// only the local store can be asked to hold a secret.
func (s *UserStore) SetPassword(ctx context.Context, id, password string) error {
	salt := randomHex(16)
	hash, err := passwordHash(password, salt)
	if err != nil {
		return err
	}
	return s.mutate(id, func(d *userDoc) { d.Salt, d.Hash = salt, hash })
}

// --- storage plumbing ---------------------------------------------------

func (s *UserStore) put(doc userDoc) error {
	db, err := s.entities.handle()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(usersBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(doc.ID), raw)
	})
}

func (s *UserStore) doc(id string) (userDoc, error) {
	db, err := s.entities.handle()
	if err != nil {
		return userDoc{}, err
	}
	var doc userDoc
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(usersBucket))
		if b == nil {
			return backends.ErrNotFound
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return backends.ErrNotFound
		}
		return json.Unmarshal(raw, &doc)
	})
	return doc, err
}

func (s *UserStore) each(fn func(userDoc) bool) error {
	db, err := s.entities.handle()
	if err != nil {
		return err
	}
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(usersBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var doc userDoc
			if err := json.Unmarshal(raw, &doc); err != nil {
				return err
			}
			if !fn(doc) {
				return errStopScan
			}
			return nil
		})
	})
	// The sentinel ends the scan; it is not a failure, and letting it
	// escape turned every early-exit lookup into an error.
	if errors.Is(err, errStopScan) {
		return nil
	}
	return err
}

// errStopScan ends a ForEach early without being a failure.
var errStopScan = errStop{}

type errStop struct{}

func (errStop) Error() string { return "stop" }

func (s *UserStore) mutate(id string, fn func(*userDoc)) error {
	db, err := s.entities.handle()
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(usersBucket))
		if b == nil {
			return backends.ErrNotFound
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return backends.ErrNotFound
		}
		var doc userDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		fn(&doc)
		next, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), next)
	})
}

func passwordHash(password, salt string) (string, error) {
	key, err := pbkdf2.Key(sha256.New, password, []byte(salt), pbkdf2Iterations, 32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

func randomHex(n int) string {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}
