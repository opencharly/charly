package agentteams

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// s3Client is a minimal S3 client (ListObjectsV2 + GetObject + PutObject) for
// the AgentTeams MinIO file system — the snapshot/hydrate round-trip's object
// mirroring. Hand-rolled AWS SigV4 (no AWS SDK in the charly binary) so the SAME
// core serves the in-venue `charly agentteams snapshot` command (no `mc` needed
// in the venue) AND the host-side `agentteams: {method: snapshot|hydrate}` verb
// methods (no `mc` on the host) — R3, one surface, two placements.
type s3Client struct {
	endpoint string // e.g. http://127.0.0.1:9000
	bucket   string
	prefix   string // storage prefix within the bucket, e.g. agentteams/agentteams-storage
	user     string
	pass     string
	http     *http.Client
}

// s3Region is the SigV4 region MinIO accepts by default (any region works; the
// credential scope must be self-consistent).
const s3Region = "us-east-1"

// newS3ClientFromEnv builds the S3 client from the AgentTeams runtime contract
// (the same env vars the controller/minio candies consume). The root credentials
// are AGENTTEAMS_MINIO_USER/PASSWORD (the factory deployment provides them to the
// Replicator worker); when the password is unset, the self-provisioned
// root-password file on the uid-1000 volume (~/.agentteams/minio/.root-password)
// is read — the SAME file the minio server and the controller's OSS client use —
// so the command also works in a container that carries the volume (the
// controller's own container).
func newS3ClientFromEnv() *s3Client {
	user := or(os.Getenv("AGENTTEAMS_MINIO_USER"), "admin")
	pass := os.Getenv("AGENTTEAMS_MINIO_PASSWORD")
	if pass == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if data, err := os.ReadFile(filepath.Join(home, ".agentteams/minio/.root-password")); err == nil {
				pass = strings.TrimSpace(string(data))
			}
		}
	}
	return &s3Client{
		endpoint: strings.TrimRight(or(os.Getenv("AGENTTEAMS_FS_ENDPOINT"), "http://127.0.0.1:9000"), "/"),
		bucket:   or(os.Getenv("AGENTTEAMS_FS_BUCKET"), "agentteams-storage"),
		prefix:   or(os.Getenv("AGENTTEAMS_STORAGE_PREFIX"), "agentteams/agentteams-storage"),
		user:     user,
		pass:     pass,
		http:     &http.Client{Timeout: 60 * time.Second},
	}
}

// listObjects returns every object key under prefix (paginated via
// continuation tokens).
func (c *s3Client) listObjects(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	continuation := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("prefix", prefix)
		if continuation != "" {
			q.Set("continuation-token", continuation)
		}
		req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/"+c.bucket+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		c.sign(req, nil, time.Now())
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("list objects: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var out listBucketResult
		if err := xml.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decode list result: %w", err)
		}
		for _, c := range out.Contents {
			keys = append(keys, c.Key)
		}
		if !out.IsTruncated || out.NextContinuationToken == "" {
			break
		}
		continuation = out.NextContinuationToken
	}
	return keys, nil
}

// getObject fetches one object's bytes.
func (c *s3Client) getObject(ctx context.Context, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/"+c.bucket+"/"+s3EscapePath(key), nil)
	if err != nil {
		return nil, err
	}
	c.sign(req, nil, time.Now())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read object %s: %w", key, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get object %s: HTTP %d: %s", key, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// putObject writes one object's bytes.
func (c *s3Client) putObject(ctx context.Context, key string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", c.endpoint+"/"+c.bucket+"/"+s3EscapePath(key), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	c.sign(req, data, time.Now())
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("put object %s: HTTP %d: %s", key, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// s3EscapePath escapes each path segment of an object key, keeping the `/`
// separators intact (S3 keys are paths, not single URL segments).
func s3EscapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// sign applies AWS Signature Version 4 to the request. With empty credentials
// the request is left unsigned (anonymous access).
func (c *s3Client) sign(req *http.Request, payload []byte, now time.Time) {
	if c.user == "" || c.pass == "" {
		return
	}
	payloadHash := sha256Hex(payload)
	amzDate := now.UTC().Format("20060102T150405Z")
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("Host", req.URL.Host)

	// Canonical request.
	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := req.URL.RawQuery
	headerVals := make(map[string]string, len(req.Header))
	for k, v := range req.Header {
		headerVals[strings.ToLower(k)] = strings.TrimSpace(strings.Join(v, ","))
	}
	signedHeaders := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		signedHeaders = append(signedHeaders, "content-type")
	}
	sort.Strings(signedHeaders)
	var canonicalHeaders strings.Builder
	for _, h := range signedHeaders {
		canonicalHeaders.WriteString(h + ":" + headerVals[h] + "\n")
	}
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders.String(),
		strings.Join(signedHeaders, ";"),
		payloadHash,
	}, "\n")

	// String to sign.
	dateStamp := now.UTC().Format("20060102")
	scope := dateStamp + "/" + s3Region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// Signing key + signature.
	kDate := hmacSHA256([]byte("AWS4"+c.pass), dateStamp)
	kRegion := hmacSHA256(kDate, s3Region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.user+"/"+scope+
		", SignedHeaders="+strings.Join(signedHeaders, ";")+", Signature="+signature)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}

// listBucketResult is the ListObjectsV2 XML response shape.
type listBucketResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
}
