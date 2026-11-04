package tako

import (
	"testing"
	"time"
)

type deviceProfile struct {
	DeviceID  string    `entity:"id" json:"device_id"`
	Theme     string    `entity:"index" json:"theme"`
	Visits    int       `json:"visits"`
	ExpiresAt time.Time `entity:"ttl" json:"expires_at"`
}

var deviceProfiles = Entity[deviceProfile]("device-profiles")

func TestEntityUpsertAndTTL(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()

	// 1. Upsert creates when absent and auto-populates the entity:"id" field.
	err := deviceProfiles.Upsert(ctx, "dev-1", func(p *deviceProfile) {
		if p.DeviceID != "dev-1" {
			t.Errorf("expected DeviceID pre-populated to dev-1, got %q", p.DeviceID)
		}
		p.Theme = "dark"
		p.Visits++
		p.ExpiresAt = time.Now().Add(time.Hour)
	})
	if err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	// 2. Upsert updates when present and not expired.
	err = deviceProfiles.Upsert(ctx, "dev-1", func(p *deviceProfile) {
		p.Visits++
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	got, err := deviceProfiles.Get(ctx, "dev-1")
	if err != nil {
		t.Fatalf("Get active profile: %v", err)
	}
	if got.Visits != 2 || got.Theme != "dark" {
		t.Fatalf("got %+v, want Visits=2 Theme=dark", got)
	}

	// 3. Expired entity is filtered on Get, Exists, Query, Count, and Update.
	err = deviceProfiles.Put(ctx, deviceProfile{
		DeviceID:  "dev-expired",
		Theme:     "dark",
		Visits:    99,
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("Put expired: %v", err)
	}

	if _, err := deviceProfiles.Get(ctx, "dev-expired"); !IsNotFound(err) {
		t.Fatalf("Get expired err = %v, want IsNotFound", err)
	}
	if ok, err := deviceProfiles.Exists(ctx, "dev-expired"); err != nil || ok {
		t.Fatalf("Exists expired = (%v, %v), want (false, nil)", ok, err)
	}
	if err := deviceProfiles.Update(ctx, "dev-expired", func(p *deviceProfile) { p.Visits++ }); !IsNotFound(err) {
		t.Fatalf("Update expired err = %v, want IsNotFound", err)
	}

	all, err := deviceProfiles.Query(ctx).Where("Theme", "=", "dark").All()
	if err != nil {
		t.Fatalf("Query All: %v", err)
	}
	if len(all) != 1 || all[0].DeviceID != "dev-1" {
		t.Fatalf("Query All returned %+v, want only dev-1", all)
	}
	cnt, err := deviceProfiles.Query(ctx).Where("Theme", "=", "dark").Count()
	if err != nil || cnt != 1 {
		t.Fatalf("Query Count = (%d, %v), want (1, nil)", cnt, err)
	}

	// 4. Upsert on an expired entity starts from a fresh zero struct.
	err = deviceProfiles.Upsert(ctx, "dev-expired", func(p *deviceProfile) {
		if p.Visits != 0 {
			t.Errorf("expected expired entity to start at Visits=0, got %d", p.Visits)
		}
		p.Theme = "light"
		p.Visits = 1
		p.ExpiresAt = time.Now().Add(time.Hour)
	})
	if err != nil {
		t.Fatalf("Upsert expired: %v", err)
	}
	revived, err := deviceProfiles.Get(ctx, "dev-expired")
	if err != nil || revived.Visits != 1 || revived.Theme != "light" {
		t.Fatalf("revived = %+v, err = %v", revived, err)
	}
}
