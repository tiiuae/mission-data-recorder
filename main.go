package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dgrijalva/jwt-go"
)

var (
	projectID           = flag.String("project-id", "auto-fleet-mgnt", "Google Cloud project id")
	deviceID            = flag.String("device-id", "", "The provisioned device id (required)")
	backendURL          = flag.String("backend-url", "", "URL to the backend server (required)")
	privateKeyPath      = flag.String("private-key", "/enclave/rsa_private.pem", "The private key used for authentication")
	privateKeyAlgorithm = flag.String("key-algorithm", "RS256", "Supported values are RS256 and ES256")
	topics              = flag.String("topics", "*", `Comma-separated list of topics to record. Special value "*" means everything.`)
	destDir             = flag.String("dest-dir", ".", "The directory where recordings are stored")
	sizeThreshold       = flag.Int("size-threshold", 10*1000*1000, "Rosbags will be split when this size in bytes is reached")
)

func loadPrivateKey() (key interface{}, err error) {
	rawKey, err := os.ReadFile(*privateKeyPath)
	if err != nil {
		return nil, err
	}
	switch *privateKeyAlgorithm {
	case "RS256":
		key, err = jwt.ParseRSAPrivateKeyFromPEM(rawKey)
	case "ES256":
		key, err = jwt.ParseECPrivateKeyFromPEM(rawKey)
	default:
		err = fmt.Errorf("unsupported key algorithm: %s", *privateKeyAlgorithm)
	}
	return key, err
}

var uploader fileUploader

func logUploadBagErr(bagPath string, err error) {
	log.Printf("failed to upload bag '%s': %s", bagPath, err.Error())
}

func uploadBag(ctx context.Context, bagPath string) {
	log.Printf("bag '%s' is ready", bagPath)
	f, err := os.Open(bagPath)
	if err != nil {
		logUploadBagErr(bagPath, err)
		return
	}
	defer f.Close()
	uploadURL, err := uploader.requestUploadURL(ctx, *backendURL+"/generate-url")
	if err != nil {
		logUploadBagErr(bagPath, err)
		return
	}
	if err = uploader.uploadFile(ctx, uploadURL, f); err != nil {
		logUploadBagErr(bagPath, err)
		return
	}
	log.Printf("bag '%s' uploaded successfully", filepath.Base(bagPath))
	if err = os.Remove(bagPath); err != nil {
		log.Printf("failed to remove uploaded bag '%s': %s", bagPath, err.Error())
	}
}

func run() int {
	flag.Parse()
	privateKey, err := loadPrivateKey()
	if err != nil {
		log.Println(err)
		return 1
	}

	var topicValues []string
	if *topics != "*" {
		topicValues = strings.Split(*topics, ",")
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-signalChan
		cancel()
	}()

	uploader = fileUploader{
		HTTPClient:    http.DefaultClient,
		SigningMethod: jwt.GetSigningMethod(*privateKeyAlgorithm),
		SigningKey:    privateKey,
		TokenLifetime: 2 * time.Minute,
		DeviceID:      *deviceID,
		ProjectID:     *projectID,
	}
	recorder := missionDataRecorder{
		Dir:           *destDir,
		Topics:        topicValues,
		SizeThreshold: *sizeThreshold,
	}
	if err = recorder.Start(ctx, uploadBag); err != nil {
		log.Println(err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run())
}
