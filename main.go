package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dgrijalva/jwt-go"
	_ "github.com/mattn/go-sqlite3"
)

const (
	defaultSizeThreshold   = 10_000_000
	defaultMaxUploadCount  = 5
	defaultCompressionMode = compressionNone
)

var (
	projectID           = flag.String("project-id", "auto-fleet-mgnt", "Google Cloud project id")
	deviceID            = flag.String("device-id", "", "The provisioned device id (required)")
	backendURL          = flag.String("backend-url", "", "URL to the backend server (required)")
	privateKeyPath      = flag.String("private-key", "/enclave/rsa_private.pem", "The private key used for authentication")
	privateKeyAlgorithm = flag.String("key-algorithm", "RS256", "Supported values are RS256 and ES256")
	topics              = flag.String("topics", "*", `Comma-separated list of topics to record. Special value "*" means everything. If empty, recording is not started.`)
	destDir             = flag.String("dest-dir", ".", "The directory where recordings are stored")
	sizeThreshold       = flag.Int("size-threshold", defaultSizeThreshold, "Rosbags will be split when this size in bytes is reached")
	extraArgs           = flag.String("extra-args", "", `Comma-separated list of extra arguments passed to ros bag record command after all other arguments passed to the command by this program.`)
	maxUploadCount      = flag.Int("max-upload-count", defaultMaxUploadCount, "Maximum number of concurrent file uploads. If zero, file uploading is disabled.")
	compression         = defaultCompressionMode
)

func init() {
	flag.Var(&compression, "compression-mode", "Compression mode to use")
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

func parseCommaSeparatedList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

var matchPatternEscaper = strings.NewReplacer(
	`*`, `\*`,
	`?`, `\?`,
	`[`, `\[`,
	`\`, `\\`,
)

func escapeMatchPattern(p string) string {
	return matchPatternEscaper.Replace(p)
}

func run() (err error) {
	flag.Parse()
	privateKey, err := loadPrivateKey()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	initialConfig := &config{
		SizeThreshold:  *sizeThreshold,
		MaxUploadCount: *maxUploadCount,
	}
	if *topics == "*" {
		initialConfig.RecordAllTopics = true
	} else if *topics != "" {
		initialConfig.Topics = parseCommaSeparatedList(*topics)
	}

	uploader := &fileUploader{
		HTTPClient:    http.DefaultClient,
		SigningMethod: jwt.GetSigningMethod(*privateKeyAlgorithm),
		SigningKey:    privateKey,
		TokenLifetime: 2 * time.Minute,
		DeviceID:      *deviceID,
		ProjectID:     *projectID,
	}
	uploadMan := newUploadManager(*maxUploadCount, uploader)
	if err = uploadMan.LoadExistingBags(*destDir); err != nil {
		log.Println("failed to load existing bags:", err)
	}

	configWatcher, err := newConfigWatcher(
		*deviceID,
		"mission_data_recorder",
		initialConfig,
	)
	if err != nil {
		return fmt.Errorf("failed to create config watcher: %w", err)
	}
	configWatcher.UploadManager = uploadMan
	configWatcher.Recorder.ExtraArgs = parseCommaSeparatedList(*extraArgs)
	configWatcher.Recorder.Dir = *destDir
	err = configWatcher.Start(ctx)
	switch err {
	case nil, context.Canceled:
		return nil
	default:
		return fmt.Errorf("config watcher stopped with an error: %w", err)
	}
}

func main() {
	if err := run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
