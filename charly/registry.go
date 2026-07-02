package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// ImageInfo contains information about a remote image
type ImageInfo struct {
	Ref       string   `json:"ref"`
	Digest    string   `json:"digest"`
	MediaType string   `json:"mediaType"`
	Platforms []string `json:"platforms,omitempty"`
}

// InspectRemoteImage fetches information about a remote image
func InspectRemoteImage(ref string) (*ImageInfo, error) {
	// Parse the reference
	imgRef, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("parsing reference %q: %w", ref, err)
	}

	// Get the manifest
	manifest, err := crane.Manifest(ref)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest for %q: %w", ref, err)
	}

	// Get the digest
	digest, err := crane.Digest(ref)
	if err != nil {
		return nil, fmt.Errorf("fetching digest for %q: %w", ref, err)
	}

	info := &ImageInfo{
		Ref:    imgRef.Name(),
		Digest: digest,
	}

	// Try to determine media type from manifest
	// The manifest is a byte slice, we can check the first few bytes
	if len(manifest) > 0 {
		// Simple heuristic - if it contains "manifests" it's likely a manifest list
		manifestStr := string(manifest)
		if contains(manifestStr, "\"manifests\"") {
			info.MediaType = "application/vnd.oci.image.index.v1+json"
		} else {
			info.MediaType = "application/vnd.oci.image.manifest.v1+json"
		}
	}

	return info, nil
}

// ImageExists checks if an image exists in the registry
func ImageExists(ref string) (bool, error) { //nolint:unparam // error return kept for interface/API stability
	_, err := crane.Digest(ref)
	if err != nil {
		// Check if it's a "not found" error
		return false, nil
	}
	return true, nil
}

// contains is a simple string contains check
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// UserInfo contains information about a user from /etc/passwd
type UserInfo struct {
	Name string
	UID  int
	GID  int
	Home string
}

// InspectImageUser inspects a remote image for a user with the given UID
// Returns the user info if found, or nil if not found
func InspectImageUser(ref string, uid int) (*UserInfo, error) {
	// Parse reference
	imgRef, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("parsing reference %q: %w", ref, err)
	}

	// Get the image
	img, err := remote.Image(imgRef)
	if err != nil {
		return nil, fmt.Errorf("fetching image %q: %w", ref, err)
	}

	// Extract /etc/passwd from the image
	passwdContent, err := extractFileFromImage(img, "etc/passwd")
	if err != nil {
		// If we can't get passwd, return nil (no user found)
		return nil, nil
	}

	// Parse passwd file to find user with matching UID
	return parsePasswdForUID(passwdContent, uid)
}

// extractFileFromImage extracts a file from an image's layers
func extractFileFromImage(img v1.Image, path string) ([]byte, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}

	// Process layers in reverse order (top layer first) to get the latest version
	for _, layer := range slices.Backward(layers) {

		reader, err := layer.Uncompressed()
		if err != nil {
			continue
		}

		content, found := findFileInTar(reader, path)
		reader.Close()
		if found {
			return content, nil
		}
	}

	return nil, fmt.Errorf("file %q not found in image", path)
}

// findFileInTar searches for a file in a tar archive
func findFileInTar(r io.Reader, targetPath string) ([]byte, bool) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		// Normalize path (remove leading /)
		name := strings.TrimPrefix(hdr.Name, "/")
		if name == targetPath {
			content, err := io.ReadAll(tr)
			if err != nil {
				return nil, false
			}
			return content, true
		}
	}
	return nil, false
}

// parsePasswdForUID parses /etc/passwd content and returns user info for matching UID
func parsePasswdForUID(content []byte, targetUID int) (*UserInfo, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// Format: name:password:uid:gid:gecos:home:shell
		parts := strings.Split(line, ":")
		if len(parts) < 6 {
			continue
		}

		uid, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}

		if uid == targetUID {
			gid, _ := strconv.Atoi(parts[3])
			return &UserInfo{
				Name: parts[0],
				UID:  uid,
				GID:  gid,
				Home: parts[5],
			}, nil
		}
	}

	return nil, nil // User not found
}
