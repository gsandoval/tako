module journal

go 1.26

require github.com/gsandoval/tako v0.0.0

require (
	cloud.google.com/go/compute/metadata v0.7.0 // indirect
	go.etcd.io/bbolt v1.5.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/gsandoval/tako => ../..
