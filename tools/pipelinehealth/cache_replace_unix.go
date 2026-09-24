//go:build !windows

package main

import "os"

func replaceCacheFile(source, destination string) error {
	// source comes from os.CreateTemp and destination from a SHA-256 cache key;
	// both are confined to the explicit operator-selected cache directory.
	//nolint:gosec // G703: neither path contains repository-controlled segments.
	return os.Rename(source, destination)
}
