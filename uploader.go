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
	"reflect"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/ulikunitz/xz"
)

var errEmptyBag = errors.New("bag is empty")

var validBagExtensions = []string{".gz", ".xz"}

type compressionMode string

const (
	compressionNone compressionMode = "none"
	compressionGzip compressionMode = "gzip"
	compressionXz   compressionMode = "xz"
)

func (m compressionMode) String() string {
	return string(m)
}

func (m compressionMode) Type() string {
	return "compression mode"
}

func (m *compressionMode) Set(val string) error {
	mode, err := m.Parse(val)
	if err != nil {
		return err
	}
	*m = mode.(compressionMode)
	return nil
}

func (m compressionMode) Parse(val interface{}) (interface{}, error) {
	if val, ok := val.(string); ok {
		switch val {
		case "none":
			return compressionNone, nil
		case "gzip":
			return compressionGzip, nil
		case "xz":
			return compressionXz, nil
		}
	}
	return nil, fmt.Errorf("invalid compression mode: %v", val)
}

type modifierFunc = func(io.Writer) (io.WriteCloser, error)

type pipe struct {
	src          io.Reader
	modifier     modifierFunc
	pipeIn       *io.PipeWriter
	pipeOut      *io.PipeReader
	closeErr     error
	closeErrChan chan struct{}
}

func newPipe(src io.Reader, modifier modifierFunc) *pipe {
	p := &pipe{
		src:          src,
		modifier:     modifier,
		closeErrChan: make(chan struct{}),
	}
	p.pipeOut, p.pipeIn = io.Pipe()
	go p.copy()
	return p
}

func (p *pipe) Read(data []byte) (int, error) {
	return p.pipeOut.Read(data)
}

func (p *pipe) copy() {
	var (
		modifier io.WriteCloser
		err      error
	)
	defer func() {
		if modifier == nil || reflect.ValueOf(modifier).IsNil() {
			p.closeErr = err
		} else {
			p.closeErr = modifier.Close()
		}
		close(p.closeErrChan)
		p.pipeIn.CloseWithError(err)
	}()
	modifier, err = p.modifier(p.pipeIn)
	if err == nil {
		_, err = io.Copy(modifier, p.src)
	}
}

func (p *pipe) Close() error {
	p.pipeOut.Close()
	<-p.closeErrChan
	return p.closeErr
}

type fileUploader struct {
	HTTPClient      *http.Client
	SigningMethod   jwt.SigningMethod
	SigningKey      interface{}
	TokenLifetime   time.Duration
	DeviceID        string
	TenantID        string
	CompressionMode compressionMode
	BackendURL      string
}

func (u *fileUploader) WithCompression(mode compressionMode) uploaderInterface {
	x := *u
	x.CompressionMode = mode
	return &x
}

func (u *fileUploader) createToken(bagName string) (string, error) {
	type jwtClaims struct {
		DeviceID string `json:"deviceId"`
		TenantID string `json:"tenantId"`
		BagName  string `json:"bagName"`
		jwt.RegisteredClaims
	}
	now := time.Now()
	token := jwt.NewWithClaims(u.SigningMethod, &jwtClaims{
		DeviceID: u.DeviceID,
		TenantID: u.TenantID,
		BagName:  bagName,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(u.TokenLifetime)),
		},
	})
	return token.SignedString(u.SigningKey)
}

func wrapErr(format string, err *error, a ...interface{}) {
	if *err != nil {
		*err = fmt.Errorf(format, append([]interface{}{*err}, a...)...)
	}
}

func (u *fileUploader) requestUploadURL(ctx context.Context, bagName, endpoint string) (_ string, err error) {
	defer wrapErr("failed to request upload URL: %w", &err)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	token, err := u.createToken(bagName)
	if err != nil {
		return "", fmt.Errorf("failed to create token: %w", err)
	}
	req.Header.Add("Authorization", "Bearer "+token)
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	var respData struct{ URL, Error string }
	if err := json.Unmarshal(body, &respData); err != nil {
		return "", fmt.Errorf("response is invalid JSON: %w: %q", err, body)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("request failed with code %d: %s", resp.StatusCode, respData.Error)
	}
	return respData.URL, nil
}

func (u *fileUploader) uploadFile(ctx context.Context, url string, file io.Reader) (err error) {
	defer wrapErr("failed to upload file: %w", &err)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, file)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	msg, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP error: code %d, %s", resp.StatusCode, msg)
	}
	return nil
}

func (u *fileUploader) withCompression(src io.Reader) (rc io.ReadCloser, ext string, err error) {
	var modifier modifierFunc
	switch u.CompressionMode {
	case compressionNone:
		return io.NopCloser(src), "", nil
	case compressionGzip:
		modifier = func(w io.Writer) (io.WriteCloser, error) {
			return gzip.NewWriter(w), nil
		}
		ext = ".gz"
	case compressionXz:
		modifier = func(w io.Writer) (io.WriteCloser, error) {
			return xz.NewWriter(w)
		}
		ext = ".xz"
	default:
		return nil, "", fmt.Errorf("invalid compression mode: %#v", u.CompressionMode)
	}
	return newPipe(src, modifier), ext, err
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
	name := recordStartTime.Format(timeFormat) + ".db3" + ext
	uploadURL, err := u.requestUploadURL(ctx, name, u.BackendURL+"/generate-url")
	if err != nil {
		return err
	}
	return u.uploadFile(ctx, uploadURL, compressed)
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
