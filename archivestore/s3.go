// Package archivestore stores immutable cold-task packages in S3-compatible storage.
package archivestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Config struct {
	Endpoint, Region, Bucket, Prefix, AccessKey, SecretKey string
	PathStyle                                              bool
	SessionToken, SSE, KMSKeyID                            string
}

type Store struct {
	client         *s3.Client
	bucket, prefix string
	encryption     types.ServerSideEncryption
	kmsKeyID       string
}

// FromEnv returns nil for the default local backend. A configured but invalid
// backend is an error, never a silent fallback to the machine's local disk.
func FromEnv() (*Store, error) {
	switch os.Getenv("ARTEX_ARCHIVE_STORAGE") {
	case "", "local":
		return nil, nil
	case "s3":
		return New(Config{
			Endpoint: os.Getenv("ARTEX_ARCHIVE_S3_ENDPOINT"), Region: os.Getenv("ARTEX_ARCHIVE_S3_REGION"),
			Bucket: os.Getenv("ARTEX_ARCHIVE_S3_BUCKET"), Prefix: os.Getenv("ARTEX_ARCHIVE_S3_PREFIX"),
			AccessKey: os.Getenv("ARTEX_ARCHIVE_S3_ACCESS_KEY_ID"), SecretKey: os.Getenv("ARTEX_ARCHIVE_S3_SECRET_ACCESS_KEY"),
			PathStyle:    os.Getenv("ARTEX_ARCHIVE_S3_PATH_STYLE") == "true",
			SessionToken: os.Getenv("ARTEX_ARCHIVE_S3_SESSION_TOKEN"),
			SSE:          os.Getenv("ARTEX_ARCHIVE_S3_SSE"), KMSKeyID: os.Getenv("ARTEX_ARCHIVE_S3_KMS_KEY_ID"),
		})
	default:
		return nil, errors.New("ARTEX_ARCHIVE_STORAGE must be local or s3")
	}
}

func New(c Config) (*Store, error) {
	u, err := url.Parse(c.Endpoint)
	loopback := false
	if err == nil {
		loopback = u.Hostname() == "localhost"
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			loopback = ip.IsLoopback()
		}
	}
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && !(u.Scheme == "http" && loopback)) {
		return nil, errors.New("archive S3 endpoint must be an HTTPS origin (HTTP allowed only on loopback)")
	}
	c.Prefix = strings.Trim(c.Prefix, "/")
	if c.Prefix == "" {
		c.Prefix = "artex/tasks"
	}
	if path.Clean(c.Prefix) != c.Prefix || strings.Contains(c.Prefix, "..") || strings.ContainsAny(c.Prefix, "\\?#") {
		return nil, errors.New("invalid archive S3 prefix")
	}
	if c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\:@?# ") {
		return nil, errors.New("a valid archive S3 bucket is required")
	}
	explicitCredentials := c.AccessKey != "" || c.SecretKey != "" || c.SessionToken != ""
	if explicitCredentials && (c.AccessKey == "" || c.SecretKey == "") {
		return nil, errors.New("archive S3 access key and secret key must both be set when overriding AWS credentials")
	}
	if c.SSE == "" {
		c.SSE = "AES256"
	}
	if c.SSE != "AES256" && c.SSE != "aws:kms" {
		return nil, errors.New("archive S3 SSE must be AES256 or aws:kms; encryption cannot be disabled")
	}
	if c.KMSKeyID != "" && c.SSE != "aws:kms" {
		return nil, errors.New("archive S3 KMS key requires SSE=aws:kms")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	httpClient := &http.Client{Transport: transport}
	cfg := aws.Config{Region: c.Region, HTTPClient: httpClient}
	if explicitCredentials {
		cfg.Credentials = credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, c.SessionToken)
	} else {
		// Keep the SDK's refreshable default chain: standard AWS environment/shared
		// profiles, web identity, ECS task roles and EC2 instance roles (IMDSv2).
		var err error
		cfg, err = awsconfig.LoadDefaultConfig(context.Background(),
			awsconfig.WithRegion(c.Region), awsconfig.WithHTTPClient(httpClient))
		if err != nil {
			return nil, fmt.Errorf("load archive S3 AWS configuration: %w", err)
		}
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(c.Endpoint)
		o.UsePathStyle = c.PathStyle
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &Store{client: client, bucket: c.Bucket, prefix: c.Prefix + "/",
		encryption: types.ServerSideEncryption(c.SSE), kmsKeyID: c.KMSKeyID}, nil
}

func (s *Store) URI(taskID, archiveID int64, checksum string) string {
	return "s3://" + s.bucket + "/" + s.prefix + fmt.Sprintf("task-%d-%d-%s.tar.zst", taskID, archiveID, checksum)
}

func (s *Store) key(uri string) (string, error) {
	if s == nil {
		return "", errors.New("S3 archive storage is not configured")
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "s3" || u.Host != s.bucket || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("archive object is outside the configured bucket")
	}
	key := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(key, s.prefix) || len(key) <= len(s.prefix) || path.Clean(key) != key || strings.Contains(key, "\\") {
		return "", errors.New("archive object is outside the configured prefix")
	}
	return key, nil
}

func (s *Store) Validate(uri string) error { _, err := s.key(uri); return err }

// Upload verifies the entire uploaded object by reading it back, including
// multipart uploads whose ETag is not a checksum. Callers retain source data
// until verification succeeds and PostgreSQL commits the remote location.
func (s *Store) Upload(ctx context.Context, uri, filename, checksum string) error {
	key, err := s.key(uri)
	if err != nil {
		return err
	}
	if decoded, err := hex.DecodeString(checksum); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid archive SHA-256")
	}
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	uploader := manager.NewUploader(s.client, func(u *manager.Uploader) {
		u.PartSize = 8 << 20
		u.Concurrency = 2
		u.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: f,
		ContentType: aws.String("application/zstd"), Metadata: map[string]string{"sha256": checksum},
		ServerSideEncryption: s.encryption,
	}
	if s.kmsKeyID != "" {
		input.SSEKMSKeyId = aws.String(s.kmsKeyID)
	}
	_, err = uploader.Upload(ctx, input)
	if err != nil {
		return fmt.Errorf("upload archive: %w", err)
	}
	return s.readVerified(ctx, key, io.Discard, checksum, info.Size(), true)
}

func (s *Store) readVerified(ctx context.Context, key string, out io.Writer, checksum string, expectedSize int64, requireEncryption bool) error {
	if decoded, err := hex.DecodeString(checksum); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid archive SHA-256")
	}
	if expectedSize < 0 {
		return errors.New("invalid archive size")
	}
	object, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("inspect archive: %w", err)
	}
	if requireEncryption && (object.ServerSideEncryption != s.encryption ||
		(s.encryption == types.ServerSideEncryptionAwsKms && aws.ToString(object.SSEKMSKeyId) == "")) {
		return errors.New("remote archive did not confirm the requested server-side encryption; source data retained")
	}
	if object.ContentLength == nil || *object.ContentLength != expectedSize || aws.ToString(object.ETag) == "" {
		return errors.New("remote archive size mismatch or missing ETag; source data retained")
	}
	// Commit only complete, validated chunks to the output and hash. A broken
	// connection can then retry its chunk without duplicating bytes or restarting
	// a multi-gigabyte read. IfMatch pins every request to the same object.
	h := sha256.New()
	for offset := int64(0); offset < expectedSize; {
		end := offset + min(archiveReadChunkSize, expectedSize-offset) - 1
		data, err := s.readArchiveChunk(ctx, key, aws.ToString(object.ETag), offset, end, expectedSize)
		if err != nil {
			return err
		}
		n, err := out.Write(data)
		if err != nil {
			return err
		}
		if n != len(data) {
			return io.ErrShortWrite
		}
		_, _ = h.Write(data)
		offset += int64(len(data))
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), checksum) {
		return errors.New("remote archive size or SHA-256 mismatch; source data retained")
	}
	return nil
}

const (
	archiveReadChunkSize = int64(8 << 20)
	archiveReadAttempts  = 5
)

func (s *Store) readArchiveChunk(ctx context.Context, key, etag string, start, end, total int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < archiveReadAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			timer := time.NewTimer((250 * time.Millisecond) << (attempt - 1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		data, retry, err := s.readArchiveChunkOnce(ctx, key, etag, start, end, total)
		if err == nil {
			return data, nil
		}
		if !retry {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("read archive bytes %d-%d after %d attempts: %w", start, end, archiveReadAttempts, lastErr)
}

func (s *Store) readArchiveChunkOnce(ctx context.Context, key, etag string, start, end, total int64) ([]byte, bool, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	object, err := s.client.GetObject(requestCtx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), IfMatch: aws.String(etag),
		Range: aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
	})
	if err != nil {
		var networkError net.Error
		retry := errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded)
		return nil, retry, fmt.Errorf("read archive range: %w", err)
	}
	defer object.Body.Close()
	want := end - start + 1
	if aws.ToString(object.ETag) != etag || aws.ToString(object.ContentRange) != fmt.Sprintf("bytes %d-%d/%d", start, end, total) || object.ContentLength == nil || *object.ContentLength != want {
		return nil, false, errors.New("remote archive changed or returned an invalid byte range; source data retained")
	}
	data, err := io.ReadAll(io.LimitReader(object.Body, want+1))
	if err != nil {
		return nil, true, err
	}
	if int64(len(data)) < want {
		return nil, true, io.ErrUnexpectedEOF
	}
	if int64(len(data)) != want {
		return nil, false, errors.New("remote archive range exceeds its declared size")
	}
	return data, false, nil
}

func (s *Store) Download(ctx context.Context, uri, filename, checksum string, size int64) (err error) {
	key, err := s.key(uri)
	if err != nil {
		return err
	}
	if size <= 0 {
		return errors.New("invalid archive size")
	}
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			_ = os.Remove(filename)
		}
	}()
	if err = s.readVerified(ctx, key, f, checksum, size, false); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Store) Delete(ctx context.Context, uri string) error {
	key, err := s.key(uri)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}
