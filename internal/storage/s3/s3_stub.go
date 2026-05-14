//go:build !s3
// +build !s3

// Package s3 provides a stub S3 backend for builds without the s3 build tag.
// To use real S3, rebuild with: go build -tags s3 ./...
package s3

import (
	"context"
	"fmt"
	"io"

	"github.com/tobibamidele/toris/internal/storage"
)

// Config holds S3 backend configuration.
type Config struct {
	Bucket    string
	Region    string
	Endpoint  string
	AccessKey string
	SecretKey string
	Prefix    string
}

// Backend is a non-functional stub that returns an error on every operation.
// Compile with -tags s3 to get the real implementation.
type Backend struct{}

// New always returns an error in stub builds.
func New(_ context.Context, _ Config) (*Backend, error) {
	return nil, fmt.Errorf(
		"S3 backend is not compiled in this build; " +
			"rebuild toris with 'go build -tags s3 ./...' to enable S3 storage",
	)
}

func (b *Backend) Name() string                                         { return "s3:stub" }
func (b *Backend) Write(_ context.Context, _ string, _ io.Reader) error { return stubErr() }
func (b *Backend) Read(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, stubErr()
}
func (b *Backend) Delete(_ context.Context, _ string) error           { return stubErr() }
func (b *Backend) List(_ context.Context, _ string) ([]string, error) { return nil, stubErr() }
func (b *Backend) Stat(_ context.Context, _ string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, stubErr()
}

func stubErr() error {
	return fmt.Errorf("S3 backend stub: rebuild with -tags s3")
}
