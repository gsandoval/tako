package gcp

import (
	"context"
	"fmt"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// Signer is the not-yet stub for signed URLs on gcp: signBlob-backed
// SignedPut/SignedGet arrive with C5.
type Signer struct{}

func (Signer) SignPut(ctx context.Context, bucket, key string, ttl time.Duration, c backends.SignConstraints) (string, error) {
	return "", fmt.Errorf("signed URLs on the gcp backend arrive with C5 (signBlob) — run locally until then")
}

func (Signer) SignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	return "", fmt.Errorf("signed URLs on the gcp backend arrive with C5 (signBlob) — run locally until then")
}
