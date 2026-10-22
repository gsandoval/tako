package gcp

// Physical resource naming, shared verbatim by the runtime (this
// package) and the provisioner (pkg/provision imports these) so the
// names services read are the names deploys create. Env isolation per
// spec §4.6a: non-prod envs prefix logical names; Datastore instead
// isolates by namespace (namespace = env, no prefix).

// DefaultEnv is the environment whose names go unprefixed. It lives
// here, next to the rule that reads it, because the runtime and the
// provisioner must agree on it exactly: a disagreement would have
// deploys create names the services cannot read.
const DefaultEnv = "prod"

// Prefixed applies the env prefix to a logical resource name.
func Prefixed(env, base string) string {
	if env == DefaultEnv {
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

// EventTopicName is the physical Pub/Sub topic a bucket's Uploaded
// events publish on. In C4 the runtime emits these after
// through-service writes; C5's GCS-native notifications take over the
// SAME topic when signed direct uploads land.
func EventTopicName(env, bucket string) string {
	return Prefixed(env, bucket+"-uploaded")
}

// DeadTopicName is the physical dead-letter topic of one async service
// (each worker/subscriber is its own service). Both the subscription's
// deadLetterPolicy and the runtime's Permanent short-circuit publish
// here.
func DeadTopicName(env, service string) string {
	return Prefixed(env, service+"-dead")
}

// DiskBucketName is the physical GCS bucket backing a persistent declared
// disk (mounted into Cloud Run via Cloud Storage FUSE). The "-disk-"
// segment keeps a disk named "media" distinct from a bucket named "media".
func DiskBucketName(project, app, env, base string) string {
	return project + "-" + app + "-disk-" + Prefixed(env, base)
}

// DiskMountPath is the absolute container path where a declared disk is
// mounted inside Cloud Run services and jobs.
func DiskMountPath(name string) string {
	return "/mnt/tako/" + name
}

