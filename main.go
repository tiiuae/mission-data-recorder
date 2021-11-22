package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v4"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/pflag"
	"github.com/tiiuae/mission-data-recorder/configloader"
	"github.com/tiiuae/rclgo/pkg/rclgo"
)

//go:generate rclgo-gen generate -d msgs --message-module-prefix github.com/tiiuae/mission-data-recorder/msgs

type logger interface {
	Infof(string, ...interface{}) error
	Errorf(string, ...interface{}) error
	Errorln(...interface{}) error
}

const timeFormat = "2006-01-02T15:04:05.000000000Z07:00"

const (
	defaultSizeThreshold   = 10_000_000
	defaultMaxUploadCount  = 5
	defaultCompressionMode = compressionNone
)

type configuration struct {
	DeviceID        string          `env:"DRONE_DEVICE_ID" usage:"The provisioned device id (required)"`
	TenantID        string          `env:"DRONE_TENANT_ID" usage:"The tenant this drone belongs to"`
	BackendURL      string          `usage:"URL to the backend server (required)"`
	PrivateKeyPath  string          `config:"private_key" flag:"private-key" env:"MISSION_DATA_RECORDER_PRIVATE_KEY" usage:"The private key used for authentication"`
	KeyAlgorithm    string          `usage:"Supported values are RS256 and ES256"`
	Topics          topicList       `usage:"Comma-separated list of topics to record. Special value \"*\" means everything. If empty, recording is not started."`
	DestDir         string          `usage:"The directory where recordings are stored"`
	SizeThreshold   int             `usage:"Rosbags will be split when this size in bytes is reached"`
	ExtraArgs       []string        `usage:"Comma-separated list of extra arguments passed to ros bag record command after all other arguments passed to the command by this program."`
	MaxUploadCount  int             `usage:"Maximum number of concurrent file uploads. If zero, file uploading is disabled."`
	CompressionMode compressionMode `usage:"Compression mode to use"`

	privateKey interface{}
	rosArgs    *rclgo.Args
}

func loadConfig() (*configuration, error) {
	config := &configuration{
		DeviceID:        "",
		TenantID:        "fleet-registry",
		BackendURL:      "",
		PrivateKeyPath:  "/enclave/rsa_private.pem",
		KeyAlgorithm:    "RS256",
		DestDir:         ".",
		SizeThreshold:   defaultSizeThreshold,
		MaxUploadCount:  defaultMaxUploadCount,
		CompressionMode: defaultCompressionMode,
	}
	rosArgs, restArgs, err := rclgo.ParseArgs(os.Args)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ROS args: %w", err)
	}
	config.rosArgs = rosArgs
	loader := configloader.New()
	loader.Args = restArgs
	loader.ConfigPath = "/enclave/mission_data_recorder.config"
	loader.ConfigType = "yaml"
	loader.EnvPrefix = "MISSION_DATA_RECORDER"
	loader.EnvFilePaths = []string{"/enclave/fog_env"}
	if err := loader.Load(config); err != nil {
		var f configloader.FatalErr
		if errors.As(err, &f) {
			return nil, err
		} else if errors.Is(err, pflag.ErrHelp) {
			return nil, nil
		}
		log.Println("during config loading:", err)
	}
	if config.DeviceID == "" {
		return nil, errors.New("device ID is required")
	}
	if config.BackendURL == "" {
		return nil, errors.New("backed URL is required")
	}
	if err := config.loadPrivateKey(); err != nil {
		return nil, err
	}
	return config, nil
}

func (config *configuration) loadPrivateKey() error {
	rawKey, err := os.ReadFile(config.PrivateKeyPath)
	if err != nil {
		return err
	}
	switch config.KeyAlgorithm {
	case "RS256":
		config.privateKey, err = jwt.ParseRSAPrivateKeyFromPEM(rawKey)
	case "ES256":
		config.privateKey, err = jwt.ParseECPrivateKeyFromPEM(rawKey)
	default:
		err = fmt.Errorf("unsupported key algorithm: %s", config.KeyAlgorithm)
	}
	return err
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
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := rclgo.GetLogger(config.DeviceID).Child("mission_data_recorder")

	initialConfig := &updatableConfig{
		Topics:          config.Topics,
		SizeThreshold:   config.SizeThreshold,
		ExtraArgs:       config.ExtraArgs,
		MaxUploadCount:  config.MaxUploadCount,
		CompressionMode: config.CompressionMode,
	}

	uploader := &fileUploader{
		HTTPClient:      http.DefaultClient,
		SigningMethod:   jwt.GetSigningMethod(config.KeyAlgorithm),
		SigningKey:      config.privateKey,
		TokenLifetime:   2 * time.Minute,
		DeviceID:        config.DeviceID,
		TenantID:        config.TenantID,
		CompressionMode: config.CompressionMode,
		BackendURL:      config.BackendURL,
	}
	uploadMan := newUploadManager(config.MaxUploadCount, uploader, logger)

	rclctx, err := rclgo.NewContext(nil, 0, config.rosArgs)
	if err != nil {
		return fmt.Errorf("failed to create rcl context: %w", err)
	}
	defer rclctx.Close()

	configWatcher, err := newConfigWatcher(
		config.DeviceID,
		"mission_data_recorder",
		initialConfig,
		rclctx,
	)
	if err != nil {
		return fmt.Errorf("failed to create config watcher: %w", err)
	}
	defer configWatcher.Close()
	configWatcher.UploadManager = uploadMan
	configWatcher.Recorder.Dir = config.DestDir
	configWatcher.Recorder.Logger = logger

	if err = uploadMan.LoadExistingBags(config.DestDir); err != nil {
		logger.Errorln("failed to load existing bags:", err)
	}
	uploadMan.StartAllWorkers(ctx)
	defer uploadMan.Wait()

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
