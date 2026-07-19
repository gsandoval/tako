// Package local implements the backend contracts in-process, with no
// emulators and no Docker: buckets on the filesystem under .tako/data/,
// entities in bbolt, queues and topics as channel dispatch.
package local
