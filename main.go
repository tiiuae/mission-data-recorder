package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/dgrijalva/jwt-go"
)

var bgctx = context.Background()

var (
	registryID = "fleet-registry"
	projectID  = "auto-fleet-mgnt"
	region     = "europe-west1"
)

var (
	deviceID            = flag.String("device-id", "", "The provisioned device id")
	backendURL          = flag.String("backend-url", "", "URL to the backend server")
	privateKeyPath      = flag.String("private-key", "/enclave/rsa_private.pem", "The private key used for authentication")
	privateKeyAlgorithm = flag.String("key-algorithm", "RS256", "supported values are RS256 and ES256")
)

func printErr(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
}

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

func run() int {
	flag.Parse()
	if flag.NArg() != 1 {
		printErr("usage:", os.Args[0], "[flags] <file>")
		flag.PrintDefaults()
		return 1
	}
	filePath := flag.Arg(0)
	privateKey, err := loadPrivateKey()
	if err != nil {
		printErr(err)
		return 1
	}

	f, err := os.Open(filePath)
	if err != nil {
		printErr(err)
		return 1
	}
	defer f.Close()

	uploader := fileUploader{
		HTTPClient:    http.DefaultClient,
		SigningMethod: jwt.GetSigningMethod(*privateKeyAlgorithm),
		SigningKey:    privateKey,
		TokenLifetime: 2 * time.Minute,
		DeviceID:      *deviceID,
	}
	uploadURL, err := uploader.requestUploadURL(bgctx, *backendURL)
	if err != nil {
		printErr(err)
		return 1
	}
	fmt.Println("got upload URL:", uploadURL)
	err = uploader.uploadFile(bgctx, uploadURL, f)
	if err != nil {
		printErr(err)
		return 1
	}
	fmt.Println("file uploaded successfully")
	return 0
}

func main() {
	os.Exit(run())
}
