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

	"github.com/golang-jwt/jwt/v4"
	"github.com/ulikunitz/xz"
	"gopkg.in/yaml.v3"
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
		if modifier == nil {
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
		jwt.RegisteredClaims
	}
	now := time.Now()
	token := jwt.NewWithClaims(u.SigningMethod, &jwtClaims{
		DeviceID: u.DeviceID,
		BagName:  bagName,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(u.TokenLifetime)),
			Audience:  jwt.ClaimStrings{u.ProjectID},
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
	uploadURL, err := u.requestUploadURL(ctx, name, backendURL+"/generate-url")
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
