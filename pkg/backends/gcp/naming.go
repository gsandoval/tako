package gcp

// Physical resource naming, shared verbatim by the runtime (this
// package) and the provisioner (pkg/provision imports these) so the
// names services read are the names deploys create. Env isolation per
// spec §4.6a: non-prod envs prefix logical names; Datastore instead
// isolates by namespace (namespace = env, no prefix).

// Prefixed applies the env prefix to a logical resource name.
func Prefixed(env, base string) string {
	if env == "prod" {
		return base
	}
	return env + "-" + base
}

// BucketName is the physical GCS bucket for a declared bucket: bucket
// names are globally unique, so the project and app scope them.
func BucketName(project, app, env, base string) string {
	return project + "-" + app + "-" + Prefixed(env, base)
}

// TopicName is the physical Pub/Sub topic for a declared queue or topic.
func TopicName(env, base string) string {
	return Prefixed(env, base)
}
