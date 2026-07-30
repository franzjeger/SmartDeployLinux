// AWS Signature V4 presigner for S3-compatible object stores.
//
// Dependency-free: the API needs exactly one operation (presigned
// PUT/GET for a single object), and SigV4 for that case is ~100 lines.
// Pulling in the full AWS SDK for it would dwarf the rest of the module.
// Works against MinIO and AWS S3 (path-style addressing, which MinIO
// requires and S3 still supports).

package s3sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Signer struct {
	// Endpoint is the base URL clients will reach the store on, e.g.
	// "http://minio:9000" or "https://s3.example.com". Path-style
	// addressing is used: <endpoint>/<bucket>/<key>.
	Endpoint  string
	Region    string // "us-east-1" for MinIO unless configured otherwise
	AccessKey string
	SecretKey string

	// Now allows tests to pin the signing time. Defaults to time.Now.
	Now func() time.Time
}

// Presign returns a presigned URL for the given method ("PUT" or "GET"),
// bucket, and object key, valid for expiry.
func (s *Signer) Presign(method, bucket, key string, expiry time.Duration) (string, error) {
	if s.Endpoint == "" || s.AccessKey == "" || s.SecretKey == "" {
		return "", fmt.Errorf("s3sign: endpoint and credentials required")
	}
	base, err := url.Parse(s.Endpoint)
	if err != nil {
		return "", fmt.Errorf("s3sign: bad endpoint: %w", err)
	}
	region := s.Region
	if region == "" {
		region = "us-east-1"
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	t := now().UTC()
	amzDate := t.Format("20060102T150405Z")
	shortDate := t.Format("20060102")
	scope := shortDate + "/" + region + "/s3/aws4_request"

	// Canonical URI: path-style (<endpoint>/<bucket>/<key>), each
	// segment URI-encoded. An empty bucket means the endpoint already
	// addresses the bucket (virtual-host style).
	canonicalURI := "/" + escapePath(key)
	if bucket != "" {
		canonicalURI = "/" + escapePath(bucket) + "/" + escapePath(key)
	}

	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", s.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", fmt.Sprintf("%d", int(expiry.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")

	canonicalQuery := canonicalQueryString(q)
	canonicalHeaders := "host:" + base.Host + "\n"
	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	signingKey := hmacSHA256(
		hmacSHA256(
			hmacSHA256(
				hmacSHA256([]byte("AWS4"+s.SecretKey), shortDate),
				region),
			"s3"),
		"aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	q.Set("X-Amz-Signature", signature)
	return base.Scheme + "://" + base.Host + canonicalURI + "?" + canonicalQueryString(q), nil
}

// canonicalQueryString sorts keys and encodes per SigV4 rules (space as
// %20, not +; slashes encoded).
func canonicalQueryString(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, sigv4Escape(k)+"="+sigv4Escape(q.Get(k)))
	}
	return strings.Join(parts, "&")
}

// escapePath encodes a path segment per SigV4: everything except
// unreserved characters, but "/" separators inside keys stay literal.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = sigv4Escape(s)
	}
	return strings.Join(segs, "/")
}

func sigv4Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}
