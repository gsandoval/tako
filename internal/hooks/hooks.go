// Package hooks is the boot seam between generated services and the SDK:
// pkg/runtime wires backends in at startup, and the root tako package
// reads them per operation. Internal on purpose — user code never touches
// backends directly.
package hooks

import "github.com/gsandoval/tako/pkg/backends"

// Entity is the wired entity backend; nil until boot.
var Entity backends.Entity
