//go:build !windows

package mtp

import (
	"context"
	"errors"

	"github.com/ngojclee/camera-connect/internal/camera/massstorage"
)

// Source is a stub on non-Windows platforms.
type Source struct{}

// Open is unsupported off Windows.
func Open(pnpID, model string) (*Source, error) { return nil, errors.New("mtp unsupported on this platform") }
func (s *Source) Connect() error                { return errors.New("mtp unsupported") }
func (s *Source) Close()                        {}
func (s *Source) ListMedia(ctx context.Context, _ string) ([]massstorage.MediaFile, error) {
	return nil, errors.New("mtp unsupported")
}
func (s *Source) CopyTo(ctx context.Context, f massstorage.MediaFile, d string, o bool) (int64, bool, error) {
	return 0, false, errors.New("mtp unsupported")
}
func (s *Source) Delete(_ massstorage.MediaFile) error { return errors.New("mtp unsupported") }
func (s *Source) SupportsDelete() bool                 { return false }
