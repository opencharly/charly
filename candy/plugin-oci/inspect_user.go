package oci

// inspect_user.go — the remote-image /etc/passwd USER-PROBE, relocated verbatim from
// charly/registry.go (the P14a OCI cutover). The build engine's adopt-user resolution
// (generate.go) probes a base image for the account at a configured uid; the go-
// containerregistry image pull lives HERE, reached via verb:oci OpRun with
// oci_op=inspect-user (a spec.ImageUserInput in, a spec.UserInfo out).

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// inspectUserLeg is the verb:oci "inspect-user" op: decode a spec.ImageUserInput, probe
// the remote image's /etc/passwd for the user at the given uid, and return a
// spec.UserInfo (Found=false when no such user / the image can't be inspected).
func inspectUserLeg(paramsJSON []byte) (*pb.InvokeReply, error) {
	var in spec.ImageUserInput
	if len(paramsJSON) > 0 {
		if err := json.Unmarshal(paramsJSON, &in); err != nil {
			return nil, fmt.Errorf("oci inspect-user: decode request: %w", err)
		}
	}
	info := inspectImageUser(in.Ref, in.UID)
	j, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("oci inspect-user: encode reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

// inspectImageUser inspects a remote image for a user with the given UID.
// Returns Found=false if not found or the image can't be inspected (the former
// nil-return convention, now the structured spec.UserInfo).
func inspectImageUser(ref string, uid int) spec.UserInfo {
	imgRef, err := name.ParseReference(ref)
	if err != nil {
		return spec.UserInfo{}
	}

	img, err := remote.Image(imgRef)
	if err != nil {
		return spec.UserInfo{}
	}

	passwdContent, err := extractFileFromImage(img, "etc/passwd")
	if err != nil {
		// If we can't get passwd, return not-found (the former nil return).
		return spec.UserInfo{}
	}

	return parsePasswdForUID(passwdContent, uid)
}

// extractFileFromImage extracts a file from an image's layers.
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
		_ = reader.Close()
		if found {
			return content, nil
		}
	}

	return nil, fmt.Errorf("file %q not found in image", path)
}

// findFileInTar searches for a file in a tar archive.
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

// parsePasswdForUID parses /etc/passwd content and returns user info for matching UID.
func parsePasswdForUID(content []byte, targetUID int) spec.UserInfo {
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
			return spec.UserInfo{
				Found: true,
				Name:  parts[0],
				UID:   uid,
				GID:   gid,
				Home:  parts[5],
			}
		}
	}

	return spec.UserInfo{} // User not found
}
