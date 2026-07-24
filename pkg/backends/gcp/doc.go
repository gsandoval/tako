// Package gcp implements the backend contracts on Google Cloud: Entity
// on Firestore in Datastore mode, Bucket on GCS, and Queue publish on
// Pub/Sub (delivery is an OIDC push subscription into /_tako/push).
// Topics arrive with C4, signed URLs (signBlob) with C5. Plain HTTP +
// ADC tokens, matching pkg/provision/gcp's convention; physical naming
// lives in naming.go and is shared with the provisioner. A runtime 403
// here is a reconciler bug — grants derive from the same graph that
// deployed the service — so errors say that instead of retrying. Never
// merges with the conformance suite red.
package gcp
