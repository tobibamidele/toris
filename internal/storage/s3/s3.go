//go:build s3
// +build s3

// Package s3 implements storage.Backend using Amazon S3.
// This backend is only compiled when the "s3" build tag is specified:
//
//	go build -tags s3 ./...
//
// Without the build tag, the stub in s3_stub.go is used instead,
// which returns a clear error when instantiated so operators know
// they need to rebuild with -tags s3 to use S3 storage.
package s3

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	torerrors "github.com/tobibamidele/toris/internal/errors"
	"github.com/tobibamidele/toris/internal/storage"
)

const (
	multipartThreshold = 100 * 1024 * 1024
	partSize           = 64 * 1024 * 1024
)

// Backend implements storage.Backend on Amazon S3.
type Backend struct {
	client *s3.Client
	bucket string
	prefix string
}

// Config holds S3 backend configuration.
type Config struct {
	Bucket    string
	Region    string
	Endpoint  string
	AccessKey string
	SecretKey string
	Prefix    string
}

// New creates an S3 Backend.
func New(ctx context.Context, cfg Config) (*Backend, error) {
	if cfg.Bucket == "" {
		return nil, torerrors.New(torerrors.CodeConfigInvalid, "s3 bucket must not be empty")
	}
	if cfg.Region == "" {
		return nil, torerrors.New(torerrors.CodeConfigInvalid, "s3 region must not be empty")
	}

	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(cfg.Region))
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, torerrors.Wrapf(torerrors.CodeConfigInvalid, err, "loading AWS config")
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		})
	}

	return &Backend{
		client: s3.NewFromConfig(awsCfg, s3Opts...),
		bucket: cfg.Bucket,
		prefix: strings.TrimSuffix(cfg.Prefix, "/"),
	}, nil
}

func (b *Backend) Name() string { return fmt.Sprintf("s3://%s/%s", b.bucket, b.prefix) }

func (b *Backend) Write(ctx context.Context, key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err, "reading source for %s", key)
	}
	if int64(len(data)) < multipartThreshold {
		return b.putObject(ctx, b.s3Key(key), data)
	}
	return b.multipartUpload(ctx, b.s3Key(key), data)
}

func (b *Backend) putObject(ctx context.Context, s3Key string, data []byte) error {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:               aws.String(b.bucket),
		Key:                  aws.String(s3Key),
		Body:                 strings.NewReader(string(data)),
		ServerSideEncryption: types.ServerSideEncryptionAes256,
	})
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err, "PutObject %s", s3Key)
	}
	return nil
}

func (b *Backend) multipartUpload(ctx context.Context, s3Key string, data []byte) error {
	create, err := b.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:               aws.String(b.bucket),
		Key:                  aws.String(s3Key),
		ServerSideEncryption: types.ServerSideEncryptionAes256,
	})
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err, "CreateMultipartUpload %s", s3Key)
	}
	uploadID := *create.UploadId

	var completedParts []types.CompletedPart
	for i := 0; i < len(data); i += partSize {
		partNum := int32(len(completedParts) + 1)
		end := i + partSize
		if end > len(data) {
			end = len(data)
		}
		up, err := b.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(b.bucket),
			Key:        aws.String(s3Key),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(partNum),
			Body:       strings.NewReader(string(data[i:end])),
		})
		if err != nil {
			_, _ = b.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket: aws.String(b.bucket), Key: aws.String(s3Key), UploadId: aws.String(uploadID),
			})
			return torerrors.Wrapf(torerrors.CodeStorageFailed, err, "UploadPart %d for %s", partNum, s3Key)
		}
		completedParts = append(completedParts, types.CompletedPart{ETag: up.ETag, PartNumber: aws.Int32(partNum)})
	}

	_, err = b.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(b.bucket),
		Key:             aws.String(s3Key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completedParts},
	})
	if err != nil {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err, "CompleteMultipartUpload %s", s3Key)
	}
	return nil
}

func (b *Backend) Read(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.s3Key(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, torerrors.Newf(torerrors.CodeNotFound, "object not found: %s", key)
		}
		return nil, torerrors.Wrapf(torerrors.CodeStorageFailed, err, "GetObject %s", key)
	}
	return out.Body, nil
}

func (b *Backend) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.s3Key(key)),
	})
	if err != nil && !isNotFound(err) {
		return torerrors.Wrapf(torerrors.CodeStorageFailed, err, "DeleteObject %s", key)
	}
	return nil
}

func (b *Backend) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
		Prefix: aws.String(b.s3Key(prefix)),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, torerrors.Wrapf(torerrors.CodeStorageFailed, err, "ListObjectsV2 prefix %s", prefix)
		}
		for _, obj := range page.Contents {
			k := strings.TrimPrefix(aws.ToString(obj.Key), b.prefix+"/")
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (b *Backend) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(b.s3Key(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return storage.ObjectInfo{}, torerrors.Newf(torerrors.CodeNotFound, "object not found: %s", key)
		}
		return storage.ObjectInfo{}, torerrors.Wrapf(torerrors.CodeStorageFailed, err, "HeadObject %s", key)
	}
	var lastMod time.Time
	if out.LastModified != nil {
		lastMod = *out.LastModified
	}
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return storage.ObjectInfo{
		Key: key, SizeBytes: size, LastModified: lastMod,
		ContentHash: strings.Trim(aws.ToString(out.ETag), `"`),
	}, nil
}

func (b *Backend) s3Key(key string) string {
	if b.prefix == "" {
		return key
	}
	return b.prefix + "/" + key
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "NotFound") || strings.Contains(msg, "404")
}
