package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/hashicorp/go-multierror"
	"github.com/ulikunitz/xz"
	"gopkg.in/yaml.v3"
)

var errEmptyBag = errors.New("bag is empty")

type compressionMode string

const (
	compressionNone compressionMode = "none"
	compressionGzip compressionMode = "gzip"
	compressionXz   compressionMode = "xz"
)

func (m compressionMode) String() string {
	return string(m)
}

func (m *compressionMode) Set(s string) error {
	switch s {
	case "none":
		*m = compressionNone
	case "gzip":
		*m = compressionGzip
	case "xz":
		*m = compressionXz
	default:
		return fmt.Errorf("unknown compression mode: %s", s)
	}
	return nil
}

func (m *compressionMode) UnmarshalYAML(val *yaml.Node) error {
	var s string
	if err := val.Decode(&s); err != nil {
		return err
	}
	return m.Set(s)
}

type pipe struct {
	src         io.Reader
	writer      io.WriteCloser
	pipeOut     *io.PipeReader
	pipeIn      *io.PipeWriter
	copyErrChan chan error
	copyErr     error
}

func newPipe(src io.Reader) *pipe {
	pipe := &pipe{src: src}
	pipe.pipeOut, pipe.pipeIn = io.Pipe()
	return pipe
}

func (p *pipe) copy() {
	defer close(p.copyErrChan)
	_, err := io.Copy(p.writer, p.src)
	p.copyErrChan <- err
}

func (p *pipe) Read(data []byte) (int, error) {
	if p.copyErr != nil {
		return 0, p.copyErr
	}
	select {
	case p.copyErr = <-p.copyErrChan:
		if p.copyErr == nil {
			p.copyErr = io.EOF
		}
		return 0, p.copyErr
	default:
		return p.pipeOut.Read(data)
	}
}

func (p *pipe) Close() error {
	return multierror.Append(
		p.writer.Close(),
		p.pipeOut.Close(),
		p.pipeIn.Close(),
	).ErrorOrNil()
}

type fileUploader struct {
	HTTPClient      *http.Client
	SigningMethod   jwt.SigningMethod
	SigningKey      interface{}
	TokenLifetime   time.Duration
	DeviceID        string
	ProjectID       string
	CompressionMode compressionMode
}

func (u *fileUploader) WithCompression(mode compressionMode) uploaderInterface {
	x := *u
	x.CompressionMode = mode
	return &x
}

func (u *fileUploader) createToken(bagName string) (string, error) {
	type jwtClaims struct {
		DeviceID string `json:"deviceId"`
		BagName  string `json:"bagName"`
		jwt.StandardClaims
	}
	now := time.Now()
	token := jwt.NewWithClaims(u.SigningMethod, &jwtClaims{
		DeviceID: u.DeviceID,
		BagName:  bagName,
		StandardClaims: jwt.StandardClaims{
			IssuedAt:  now.Unix(),
			ExpiresAt: now.Add(u.TokenLifetime).Unix(),
			Audience:  u.ProjectID,
		},
	})
	signedToken, err := token.SignedString(u.SigningKey)
	return signedToken, err
}

func uploadURLErr(err error) error {
	return fmt.Errorf("failed to request upload URL: %w", err)
}

func (u *fileUploader) requestUploadURL(ctx context.Context, bagName, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	if err != nil {
		return "", uploadURLErr(err)
	}
	token, err := u.createToken(bagName)
	if err != nil {
		return "", uploadURLErr(err)
	}
	req.Header.Add("Authorization", "Bearer "+token)
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return "", uploadURLErr(err)
	}
	defer resp.Body.Close()
	var respData struct {
		URL, Error string
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		io.Copy(io.Discard, resp.Body)
		return "", uploadURLErr(err)
	}
	if resp.StatusCode != 200 {
		return "", uploadURLErr(errors.New(respData.Error))
	}
	return respData.URL, nil
}

func uploadFileErr(err error) error {
	return fmt.Errorf("failed to upload file: %w", err)
}

func (u *fileUploader) uploadFile(ctx context.Context, url string, file io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", url, file)
	if err != nil {
		return uploadFileErr(err)
	}
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return uploadFileErr(err)
	}
	defer resp.Body.Close()
	msg, err := io.ReadAll(resp.Body)
	if err != nil {
		return uploadFileErr(err)
	}
	if resp.StatusCode != 200 {
		return uploadFileErr(fmt.Errorf("HTTP error: code %d, %s", resp.StatusCode, msg))
	}
	return nil
}

func (u *fileUploader) withCompression(src io.Reader) (rc io.ReadCloser, ext string, err error) {
	pipe := newPipe(src)
	defer onErr(&err, pipe.Close)
	switch u.CompressionMode {
	case compressionNone:
		return io.NopCloser(src), "", nil
	case compressionGzip:
		pipe.writer = gzip.NewWriter(pipe.pipeIn)
		ext = ".gz"
	case compressionXz:
		pipe.writer, err = xz.NewWriter(pipe.pipeIn)
		if err != nil {
			return nil, "", err
		}
		ext = ".xz"
	default:
		return nil, "", fmt.Errorf("invalid compression mode: %v", u.CompressionMode)
	}
	go pipe.copy()
	return pipe, ext, nil
}

func (u *fileUploader) UploadBag(ctx context.Context, bag *bagMetadata) error {
	f, err := os.Open(bag.path)
	if err != nil {
		return err
	}
	defer f.Close()
	compressed, ext, err := u.withCompression(f)
	if err != nil {
		return err
	}
	defer compressed.Close()
	recordStartTime, err := getRecordStartTime(ctx, bag.path)
	if err != nil {
		return err
	}
	name := recordStartTime.Format(time.RFC3339) + ".db3" + ext
	uploadURL, err := u.requestUploadURL(ctx, name, backendURL+"/generate-url")
	if err != nil {
		return err
	}
	return u.uploadFile(ctx, uploadURL, f)
}

func getRecordStartTime(ctx context.Context, bagPath string) (time.Time, error) {
	db, err := sql.Open("sqlite3", bagPath)
	if err != nil {
		return time.Time{}, err
	}
	defer db.Close()
	var timestamp int64
	err = db.QueryRowContext(ctx, "SELECT timestamp FROM messages ORDER BY timestamp LIMIT 1").Scan(&timestamp)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, errEmptyBag
		}
		return time.Time{}, err
	}
	return time.Unix(0, timestamp).UTC(), nil
}
