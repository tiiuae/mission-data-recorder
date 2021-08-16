package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/dgrijalva/jwt-go"
	"golang.org/x/sync/semaphore"
)

type fileUploader struct {
	HTTPClient    *http.Client
	SigningMethod jwt.SigningMethod
	SigningKey    interface{}
	TokenLifetime time.Duration
	DeviceID      string
	ProjectID     string
	UploadCount   *semaphore.Weighted
}

func (u *fileUploader) createToken() (string, error) {
	type jwtClaims struct {
		DeviceID string `json:"deviceId"`
		jwt.StandardClaims
	}
	now := time.Now()
	token := jwt.NewWithClaims(u.SigningMethod, &jwtClaims{
		DeviceID: u.DeviceID,
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

func (u *fileUploader) requestUploadURL(ctx context.Context, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	if err != nil {
		return "", uploadURLErr(err)
	}
	token, err := u.createToken()
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

func (u *fileUploader) uploadBagFile(ctx context.Context, bagPath string) error {
	if err := u.UploadCount.Acquire(ctx, 1); err != nil {
		return err
	}
	defer u.UploadCount.Release(1)
	f, err := os.Open(bagPath)
	if err != nil {
		return err
	}
	defer f.Close()
	uploadURL, err := u.requestUploadURL(ctx, *backendURL+"/generate-url")
	if err != nil {
		return err
	}
	return u.uploadFile(ctx, uploadURL, f)
}

func (u *fileUploader) UploadBag(ctx context.Context, bagPath string) {
	log.Printf("bag '%s' is ready", bagPath)
	if err := u.uploadBagFile(ctx, bagPath); err != nil {
		log.Printf("failed to upload bag '%s': %v", bagPath, err)
		return
	}
	log.Printf("bag '%s' uploaded successfully", filepath.Base(bagPath))
	matches, err := filepath.Glob(escapeMatchPattern(bagPath) + "*")
	if err != nil {
		log.Printf("failed to remove files for '%s': %v", bagPath, err)
		return
	}
	for _, match := range matches {
		if err = os.Remove(match); err != nil {
			log.Printf("failed to remove '%s': %v", bagPath, err)
		}
	}
}
