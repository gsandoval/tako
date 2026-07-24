package gcp

import "testing"

func TestNaming(t *testing.T) {
	if got := BucketName("my-proj", "gallery", "prod", "photos"); got != "my-proj-gallery-photos" {
		t.Errorf("prod bucket = %q", got)
	}
	if got := BucketName("my-proj", "gallery", "pr-42", "photos"); got != "my-proj-gallery-pr-42-photos" {
		t.Errorf("env bucket = %q", got)
	}
	if got := TopicName("prod", "clicks"); got != "clicks" {
		t.Errorf("prod topic = %q", got)
	}
	if got := TopicName("pr-42", "clicks"); got != "pr-42-clicks" {
		t.Errorf("env topic = %q", got)
	}
}
