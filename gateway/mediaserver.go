package gateway

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/logging"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"github.com/sirupsen/logrus"
)

type mediaServer interface {
	handleFilesUpload(fi *config.FileInfo) (string, error)
}

type commonMediaServer struct {
	logger *logrus.Entry
}

type httpPutMediaServer struct {
	commonMediaServer

	httpUploadPath     string
	httpDownloadPrefix string
}

type localMediaServer struct {
	commonMediaServer

	localPath          string
	httpDownloadPrefix string
}

type s3MediaServer struct {
	commonMediaServer

	s3Client        *s3.Client
	presignS3Client *s3.PresignClient

	cfg *s3Config
}

type s3Config struct {
	endpoint  string
	accessKey string
	secretKey string

	forcePathStyle     bool
	bucket             string
	uploadPrefix       string
	httpDownloadPrefix string
	region             string
}

var _ mediaServer = (*httpPutMediaServer)(nil)
var _ mediaServer = (*localMediaServer)(nil)
var _ mediaServer = (*s3MediaServer)(nil)

const mediaUploadTimeout = 5 * time.Second
const mediaUploadPresignDuration = 7 * 24 * time.Hour // presigned URL valid duration

var ErrMediaConfiguration = errors.New("media server is not properly configured")
var ErrMediaConfigurationNotWanted = errors.New("media server is not configured and not wanted")

var ErrMediaServerRuntime = errors.New("media server error")
var errUploadFailed = fmt.Errorf("%w: upload failed", ErrMediaServerRuntime)

func checkS3Config(bg *config.BridgeValues, uri *url.URL, logger *logrus.Entry) (*s3Config, error) {
	if bg.General.S3Bucket == "" {
		return nil, fmt.Errorf("%w: s3 bucket is not configured", ErrMediaConfiguration)
	}

	if bg.General.S3Region == "" {
		return nil, fmt.Errorf("%w: s3 region is not configured", ErrMediaConfiguration)
	}

	if bg.General.S3Endpoint == "" {
		return nil, fmt.Errorf("%w: s3 endpoint is not configured", ErrMediaConfiguration)
	}

	if bg.General.S3AccessKey == "" {
		return nil, fmt.Errorf("%w: s3 access key is not configured", ErrMediaConfiguration)
	}

	if bg.General.S3SecretKey == "" {
		return nil, fmt.Errorf("%w: s3 secret key is not configured", ErrMediaConfiguration)
	}

	if !bg.General.S3ForcePathStyle {
		logger.Warn("S3ForcePathStyle is disabled. Most S3 servers require this setting to be enabled.")
	}

	return &s3Config{
		endpoint:  bg.General.S3Endpoint,
		accessKey: bg.General.S3AccessKey,
		secretKey: bg.General.S3SecretKey,

		forcePathStyle:     bg.General.S3ForcePathStyle,
		bucket:             bg.General.S3Bucket,
		uploadPrefix:       strings.Trim(uri.Path, "/"),
		httpDownloadPrefix: bg.General.MediaServerDownload,
		region:             bg.General.S3Region,
	}, nil
}

func createS3MediaServer(bg *config.BridgeValues, uri *url.URL, logger *logrus.Entry) (*s3MediaServer, error) {
	s3Cfg, err := checkS3Config(bg, uri, logger)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(aws.Config{
		Region:       s3Cfg.region,
		Credentials:  credentials.NewStaticCredentialsProvider(s3Cfg.accessKey, s3Cfg.secretKey, ""),
		Logger:       logging.Nop{},
		BaseEndpoint: aws.String(s3Cfg.endpoint),
		HTTPClient:   &http.Client{Timeout: mediaUploadTimeout},
	}, func(o *s3.Options) {
		o.UsePathStyle = s3Cfg.forcePathStyle
	})

	var presignClient *s3.PresignClient
	if bg.General.S3Presign {
		presignClient = s3.NewPresignClient(client)
	}

	// This will return an error if the bucket does not exist
	headBucketResult, err := client.HeadBucket(context.TODO(), &s3.HeadBucketInput{Bucket: aws.String(s3Cfg.bucket)})
	if err != nil {
		return nil, fmt.Errorf("%w: failed to check if bucket exists: %w", ErrMediaServerRuntime, err)
	}

	logger.WithFields(logrus.Fields{
		"bucket":           s3Cfg.bucket,
		"uploadPrefix":     s3Cfg.uploadPrefix,
		"baseUrl":          s3Cfg.endpoint,
		"pathStyle":        s3Cfg.forcePathStyle,
		"headBucketResult": headBucketResult,
	}).Debug("checked destination bucket")

	return &s3MediaServer{
		commonMediaServer: commonMediaServer{
			logger: logger,
		},

		s3Client:        client,
		presignS3Client: presignClient,

		cfg: s3Cfg,
	}, nil
}

func createMediaServer(bg *config.BridgeValues, logger *logrus.Entry) (mediaServer, error) {
	if bg.General.MediaServerUpload == "" && bg.General.MediaDownloadPath == "" && bg.General.S3Endpoint == "" {
		return nil, ErrMediaConfigurationNotWanted //  we don't have a attachfield or we don't have a mediaserver configured return
	}

	if bg.General.S3Endpoint != "" {
		parsed, err := url.Parse(bg.General.S3Endpoint)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid S3 endpoint URL: %w", ErrMediaConfiguration, err)
		}

		s3MediaServer, err := createS3MediaServer(bg, parsed, logger.WithField("component", "s3mediaserver"))
		if err == nil {
			return s3MediaServer, nil
		}

		return nil, fmt.Errorf("%w: %w", ErrMediaConfiguration, err)
	}

	if bg.General.MediaServerUpload != "" {
		parsed, err := url.Parse(bg.General.MediaServerUpload)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid media server upload URL: %w", ErrMediaConfiguration, err)
		}

		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			return &httpPutMediaServer{
				commonMediaServer: commonMediaServer{
					logger: logger.WithField("component", "httpputmediaserver"),
				},

				httpUploadPath:     bg.General.MediaServerUpload,
				httpDownloadPrefix: bg.General.MediaServerDownload,
			}, nil
		}

		return nil, fmt.Errorf("%w: unknown schema (protocol) for mediaServerUpload: '%s'", ErrMediaConfiguration, parsed.Scheme)
	}

	if bg.General.MediaDownloadPath != "" {
		return &localMediaServer{
			commonMediaServer: commonMediaServer{
				logger: logger.WithField("component", "localmediaserver"),
			},

			localPath:          bg.General.MediaDownloadPath,
			httpDownloadPrefix: bg.General.MediaServerDownload,
		}, nil
	}

	return nil, ErrMediaConfigurationNotWanted // never reached
}

// handleFilesUpload which uses MediaServerUpload configuration to upload the file via HTTP PUT request.
// Returns error on failure.
func (h *httpPutMediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	client := &http.Client{
		Timeout: mediaUploadTimeout,
	}
	// Use MediaServerUpload. Upload using a PUT HTTP request and basicauth.
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8] //nolint:gosec
	uploadUrl := h.httpUploadPath + "/" + path.Join(sha1sum, fi.Name)

	req, err := http.NewRequest(http.MethodPut, uploadUrl, bytes.NewReader(*fi.Data))
	if err != nil {
		return "", fmt.Errorf("%w: could not create request: %w", errUploadFailed, err)
	}

	h.logger.Debugf("mediaserver upload url: %s", uploadUrl)

	req.Header.Set("Content-Type", "binary/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: could not Do request: %w", errUploadFailed, err)
	}

	err = resp.Body.Close()
	if err != nil {
		h.logger.WithError(err).Error("failed to close response body")
	}

	return h.httpDownloadPrefix + "/" + path.Join(sha1sum, fi.Name), nil
}

// handleFilesUpload which uses MediaServerPath configuration, places the file on the current filesystem.
// Returns error on failure.
func (h *localMediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8] //nolint:gosec
	dir := path.Join(h.localPath, sha1sum)

	err := os.Mkdir(dir, 0755) //nolint:gosec // this is for writing media files, so 0755 is fine, we want them to be accesible by webserver
	if err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("%w: could not mkdir: %w", errUploadFailed, err)
	}

	fileWritePath := path.Join(dir, fi.Name)
	h.logger.WithField("fileWritePath", fileWritePath).Debug("mediaserver path placing file")

	err = os.WriteFile(fileWritePath, *fi.Data, 0644) //nolint:gosec // this is for writing media files, so 0644 is fine, we want them to be accesible by webserver
	if err != nil {
		return "", fmt.Errorf("%w: could not writefile: %w", errUploadFailed, err)
	}

	return h.httpDownloadPrefix + "/" + path.Join(sha1sum, fi.Name), nil
}

// handleFilesUpload which uploads media to s3 compatible server.
// Returns error on failure.
func (h *s3MediaServer) handleFilesUpload(fi *config.FileInfo) (string, error) {
	sha1sum := fmt.Sprintf("%x", sha1.Sum(*fi.Data))[:8] //nolint:gosec
	key := path.Join(h.cfg.uploadPrefix, sha1sum, fi.Name)
	objectSize := int64(len(*fi.Data)) // TODO: Using this, sine we got this in memory anyway. Would be nicer to use fi.Size, but it is 0

	// We do not bother with multipart uploads for now, as files are expected to be small (less than 5GB).
	// If needed, we can implement that later.
	info, err := h.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(h.cfg.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(*fi.Data),
		ContentLength: aws.Int64(objectSize),
		ContentType:   aws.String("application/octet-stream"),
	})
	if err != nil {
		return "", fmt.Errorf("%w: mediaserver s3 PutObject failed: %w", errUploadFailed, err)
	}

	downloadURL := h.cfg.httpDownloadPrefix + "/" + key
	// If presign is enabled, generate a presigned URL, otherwise use the standard download URL.
	if h.presignS3Client != nil {
		downloadReq, err := h.presignS3Client.PresignGetObject(context.TODO(), &s3.GetObjectInput{
			Bucket: aws.String(h.cfg.bucket),
			Key:    aws.String(key),
		}, s3.WithPresignExpires(mediaUploadPresignDuration))
		if err != nil {
			return "", fmt.Errorf("%w: mediaserver s3 presign request creation failed: %w", errUploadFailed, err)
		}

		downloadURL = downloadReq.URL
	}

	h.logger.WithFields(logrus.Fields{
		"key":         key,
		"etag":        info.ETag,
		"downloadURL": downloadURL,
	}).Debug("successfully uploaded")

	return downloadURL, nil
}
