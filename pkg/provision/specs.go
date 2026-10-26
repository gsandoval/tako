package provision

// MountSpec is the canonical desired-state representation of one volume
// mount attached to a Cloud Run service or job.
type MountSpec struct {
	Name      string
	MountPath string
	Ephemeral bool
	SizeLimit string
	Bucket    string
	ReadOnly  bool
}

// ToMap serializes MountSpec into the canonical map[string]any shape
// hashed by SpecHash and consumed by the gcp appliers.
func (m MountSpec) ToMap() map[string]any {
	if m.Ephemeral {
		return map[string]any{
			"name":      m.Name,
			"mountPath": m.MountPath,
			"ephemeral": true,
			"sizeLimit": m.SizeLimit,
		}
	}
	return map[string]any{
		"name":      m.Name,
		"mountPath": m.MountPath,
		"ephemeral": false,
		"bucket":    m.Bucket,
		"readOnly":  m.ReadOnly,
	}
}

// RunJobSpec is the canonical desired-state representation of a Cloud
// Run v2 job.
type RunJobSpec struct {
	Image          string
	ServiceAccount string
	Env            map[string]any
	TaskCount      int
	CPU            string
	Memory         string
	Timeout        int
	Retries        int
	Mounts         []any
}

// ToMap serializes RunJobSpec into the canonical map[string]any shape
// hashed by SpecHash and reconstructed by jobLiveHash.
func (s RunJobSpec) ToMap() map[string]any {
	out := map[string]any{
		"image":          s.Image,
		"serviceAccount": s.ServiceAccount,
		"env":            s.Env,
		"taskCount":      s.TaskCount,
		"cpu":            s.CPU,
		"memory":         s.Memory,
		"timeout":        s.Timeout,
		"retries":        s.Retries,
	}
	if len(s.Mounts) > 0 {
		out["mounts"] = s.Mounts
	}
	return out
}

// RunServiceSpec is the canonical desired-state representation of a
// Cloud Run v2 service.
type RunServiceSpec struct {
	Image          string
	ServiceAccount string
	Env            map[string]any
	Scaling        map[string]any
	Public         bool
	Mounts         []any
}

// ToMap serializes RunServiceSpec into the canonical map[string]any
// shape hashed by SpecHash and reconstructed by liveHash.
func (s RunServiceSpec) ToMap() map[string]any {
	out := map[string]any{
		"image":          s.Image,
		"serviceAccount": s.ServiceAccount,
		"env":            s.Env,
		"scaling":        s.Scaling,
		"public":         s.Public,
	}
	if len(s.Mounts) > 0 {
		out["mounts"] = s.Mounts
	}
	return out
}
