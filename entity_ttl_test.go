package tako

import (
	"bytes"
	"sync/atomic"
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

type linkedAudioMemo struct {
	ID        string    `entity:"id,blob:test-memo-audio" json:"id"`
	ExpiresAt time.Time `entity:"ttl" json:"expires_at"`
}

var (
	testMemoAudio = Bucket("test-memo-audio", BucketConfig{TTL: 7 * 24 * time.Hour})
	testMemos     = Entity[linkedAudioMemo]("test-linked-memos")
)

type gatherJobParams struct {
	RunID string `json:"run_id"`
}

type gatherShard struct {
	Task int `json:"task"`
}

var (
	testGatherJob     = Job[gatherJobParams]("test-gather-job")
	testGatherWinners atomic.Int32
	testGatherSum     atomic.Int32

	_ = Runner(testGatherJob, func(ctx Ctx, p gatherJobParams) error {
		idx, _ := ctx.Task()
		all, complete, err := Gather(ctx, p.RunID, gatherShard{Task: idx + 1})
		if err != nil {
			return err
		}
		if complete {
			testGatherWinners.Add(1)
			sum := 0
			for _, s := range all {
				sum += s.Task
			}
			testGatherSum.Store(int32(sum))
		}
		return nil
	}, RunnerConfig{Parallel: 3})
)

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

func TestEntityBlobCascadeAndReapExpired(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()

	// 1. Delete cascades to the linked bucket object.
	if err := testMemoAudio.Put(ctx, "m-del", Blob{Reader: bytes.NewReader([]byte("audio-1")), ContentType: "audio/webm", Size: 7}); err != nil {
		t.Fatalf("Put blob m-del: %v", err)
	}
	if err := testMemos.Put(ctx, linkedAudioMemo{ID: "m-del", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Put entity m-del: %v", err)
	}
	if err := testMemos.Delete(ctx, "m-del"); err != nil {
		t.Fatalf("Delete m-del: %v", err)
	}
	if ok, err := testMemoAudio.Exists(ctx, "m-del"); err != nil || ok {
		t.Fatalf("blob m-del after entity Delete = (%v, %v), want (false, nil)", ok, err)
	}

	// 2. ReapExpired deletes expired entities and their linked blobs while keeping active ones.
	if err := testMemoAudio.Put(ctx, "m-exp", Blob{Reader: bytes.NewReader([]byte("audio-exp")), ContentType: "audio/webm", Size: 9}); err != nil {
		t.Fatalf("Put blob m-exp: %v", err)
	}
	if err := testMemos.Put(ctx, linkedAudioMemo{ID: "m-exp", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatalf("Put entity m-exp: %v", err)
	}
	if err := testMemoAudio.Put(ctx, "m-live", Blob{Reader: bytes.NewReader([]byte("audio-live")), ContentType: "audio/webm", Size: 10}); err != nil {
		t.Fatalf("Put blob m-live: %v", err)
	}
	if err := testMemos.Put(ctx, linkedAudioMemo{ID: "m-live", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Put entity m-live: %v", err)
	}

	reaped, err := testMemos.ReapExpired(ctx)
	if err != nil || reaped != 1 {
		t.Fatalf("ReapExpired = (%d, %v), want (1, nil)", reaped, err)
	}
	if ok, err := testMemoAudio.Exists(ctx, "m-exp"); err != nil || ok {
		t.Fatalf("expired blob m-exp still exists = (%v, %v)", ok, err)
	}
	if ok, err := testMemoAudio.Exists(ctx, "m-live"); err != nil || !ok {
		t.Fatalf("active blob m-live missing = (%v, %v)", ok, err)
	}
}

func TestSignedUploadAndGather(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()

	up, err := testMemoAudio.SignedUpload(ctx, "clip-1", 15*time.Minute, PutPolicy{
		MaxSize:     1024,
		ContentType: "audio/webm",
	})
	if err != nil {
		t.Fatalf("SignedUpload: %v", err)
	}
	if up.URL == "" || up.Headers["Content-Type"] != "audio/webm" || up.Headers["x-goog-content-length-range"] != "0,1024" {
		t.Fatalf("unexpected SignedUpload: %+v", up)
	}

	testGatherWinners.Store(0)
	testGatherSum.Store(0)
	if err := testGatherJob.Run(ctx, gatherJobParams{RunID: "run-1"}); err != nil {
		t.Fatalf("testGatherJob.Run: %v", err)
	}
	app.DrainQueues()
	if got := testGatherWinners.Load(); got != 1 {
		t.Fatalf("Gather winners = %d, want 1", got)
	}
	if got := testGatherSum.Load(); got != 6 {
		t.Fatalf("Gather sum = %d, want 6 (1+2+3)", got)
	}
}
