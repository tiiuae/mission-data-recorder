package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/dgrijalva/jwt-go"
	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/yaml.v3"
)

type stringSlice []string

func (s stringSlice) String() string {
	return strings.Join(s, ",")
}

func (s *stringSlice) Set(val string) error {
	*s = parseCommaSeparatedList(val)
	return nil
}

func (s *stringSlice) UnmarshalYAML(val *yaml.Node) error {
	var data []string
	err := val.Decode(&data)
	if err == nil {
		*s = stringSlice(data)
		return nil
	}
	var str string
	if err = val.Decode(&str); err != nil {
		return err
	}
	return s.Set(str)
}

const (
	defaultSizeThreshold   = 10_000_000
	defaultMaxUploadCount  = 5
	defaultCompressionMode = compressionNone
)

var (
	configPath = "/enclave/mission_data_recorder.config"

	projectID           = "auto-fleet-mgnt"
	deviceID            = ""
	backendURL          = ""
	privateKeyPath      = "/enclave/rsa_private.pem"
	privateKeyAlgorithm = "RS256"
	topics              topicList
	destDir             = "."
	sizeThreshold       = defaultSizeThreshold
	extraArgs           stringSlice
	maxUploadCount      = defaultMaxUploadCount
	compression         = defaultCompressionMode
)

func init() {
	flag.StringVar(&configPath, "config", configPath, "Path to config file")

	flag.StringVar(&projectID, "project-id", projectID, "Google Cloud project id")
	flag.StringVar(&deviceID, "device-id", deviceID, "The provisioned device id (required)")
	flag.StringVar(&backendURL, "backend-url", backendURL, "URL to the backend server (required)")
	flag.StringVar(&privateKeyPath, "private-key", privateKeyPath, "The private key used for authentication")
	flag.StringVar(&privateKeyAlgorithm, "key-algorithm", privateKeyAlgorithm, "Supported values are RS256 and ES256")
	flag.Var(&topics, "topics", `Comma-separated list of topics to record. Special value "*" means everything. If empty, recording is not started.`)
	flag.StringVar(&destDir, "dest-dir", destDir, "The directory where recordings are stored")
	flag.IntVar(&sizeThreshold, "size-threshold", sizeThreshold, "Rosbags will be split when this size in bytes is reached")
	flag.Var(&extraArgs, "extra-args", `Comma-separated list of extra arguments passed to ros bag record command after all other arguments passed to the command by this program.`)
	flag.IntVar(&maxUploadCount, "max-upload-count", maxUploadCount, "Maximum number of concurrent file uploads. If zero, file uploading is disabled.")
	flag.Var(&compression, "compression-mode", "Compression mode to use")
}

func parseConfig() {
	yamlBytes, err := ioutil.ReadFile(configPath)
	if err != nil {
		return
	}

	config := struct {
		Audience            *string          `yaml:"audience"`
		DeviceID            *string          `yaml:"device_id"`
		BackendURL          *string          `yaml:"backend_url"`
		PrivateKeyPath      *string          `yaml:"private_key"`
		PrivateKeyAlgorithm *string          `yaml:"key_algorithm"`
		Topics              *topicList       `yaml:"topics"`
		DestDir             *string          `yaml:"dest_dir"`
		SizeThreshold       *int             `yaml:"size_threshold"`
		ExtraArgs           *stringSlice     `yaml:"extra_args"`
		MaxUploadCount      *int             `yaml:"max_upload_count"`
		CompressionMode     *compressionMode `yaml:"compression_mode"`
	}{}

	err = yaml.Unmarshal(yamlBytes, &config)
	if err != nil {
		log.Fatalf("Failed to unmarshal config yaml: %v", err)
	}

	set := func(dst, src interface{}) {
		if s := reflect.ValueOf(src); !s.IsNil() {
			reflect.ValueOf(dst).Elem().Set(s.Elem())
		}
	}

	set(&projectID, config.Audience)
	set(&deviceID, config.DeviceID)
	set(&backendURL, config.BackendURL)
	set(&privateKeyPath, config.PrivateKeyPath)
	set(&privateKeyAlgorithm, config.PrivateKeyAlgorithm)
	set(&topics, config.Topics)
	set(&destDir, config.DestDir)
	set(&sizeThreshold, config.SizeThreshold)
	set(&extraArgs, config.ExtraArgs)
	set(&maxUploadCount, config.MaxUploadCount)
	set(&compression, config.CompressionMode)
}

func loadPrivateKey() (key interface{}, err error) {
	rawKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, err
	}
	switch privateKeyAlgorithm {
	case "RS256":
		key, err = jwt.ParseRSAPrivateKeyFromPEM(rawKey)
	case "ES256":
		key, err = jwt.ParseECPrivateKeyFromPEM(rawKey)
	default:
		err = fmt.Errorf("unsupported key algorithm: %s", privateKeyAlgorithm)
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
	flag.Parse()  // Gives us access to -config parameter
	parseConfig() // Load settings from config file
	flag.Parse()  // Prefer command line settings to config file
	privateKey, err := loadPrivateKey()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	initialConfig := &config{
		Topics:          topics,
		SizeThreshold:   sizeThreshold,
		ExtraArgs:       extraArgs,
		MaxUploadCount:  maxUploadCount,
		CompressionMode: compression,
	}

	uploader := &fileUploader{
		HTTPClient:      http.DefaultClient,
		SigningMethod:   jwt.GetSigningMethod(privateKeyAlgorithm),
		SigningKey:      privateKey,
		TokenLifetime:   2 * time.Minute,
		DeviceID:        deviceID,
		ProjectID:       projectID,
		CompressionMode: compression,
	}
	uploadMan := newUploadManager(maxUploadCount, uploader)
	if err = uploadMan.LoadExistingBags(destDir); err != nil {
		log.Println("failed to load existing bags:", err)
	}
	uploadMan.StartAllWorkers(ctx)

	configWatcher, err := newConfigWatcher(
		deviceID,
		"mission_data_recorder",
		initialConfig,
	)
	if err != nil {
		return fmt.Errorf("failed to create config watcher: %w", err)
	}
	configWatcher.UploadManager = uploadMan
	configWatcher.Recorder.Dir = destDir
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
