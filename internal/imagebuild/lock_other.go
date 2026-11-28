//go:build !unix

// SPEC: _spec/internal/maintain/image-build-deploy.puml
package imagebuild

// lockFile does not lock where flock is missing; image builds there run one host builder at a time anyway.
func lockFile(string) (func(), error) { return func() {}, nil }
