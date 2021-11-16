package configloader

import (
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"time"
	"unicode"

	"github.com/hashicorp/go-multierror"
	"github.com/joho/godotenv"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Option interface {
	Parse(interface{}) (interface{}, error)
	Type() string
}

func getOptionIfImplemented(val reflect.Value) (Option, bool) {
	if valOpt, ok := val.Interface().(Option); ok {
		return valOpt, true
	}
	if val.CanAddr() {
		if valOpt, ok := val.Addr().Interface().(Option); ok {
			return valOpt, true
		}
	}
	return nil, false
}

func getPflagValueIfImplemented(val reflect.Value) (pflag.Value, bool) {
	if valOpt, ok := val.Interface().(pflag.Value); ok {
		return valOpt, true
	}
	if val.CanAddr() {
		if valOpt, ok := val.Addr().Interface().(pflag.Value); ok {
			return valOpt, true
		}
	}
	return nil, false
}

func optionDecodeHook() mapstructure.DecodeHookFunc {
	return func(from, to reflect.Value) (interface{}, error) {
		if from.Type() == to.Type() {
			return from.Interface(), nil
		}
		if option, ok := getOptionIfImplemented(to); ok {
			return option.Parse(from.Interface())
		}
		return from.Interface(), nil
	}
}

func convertFieldName(name string, delim rune, conv func(rune) rune) string {
	var result []rune
	prevUpper := false
	for _, r := range name {
		if unicode.IsUpper(r) {
			if !prevUpper {
				prevUpper = true
				if len(result) > 0 {
					result = append(result, delim)
				}
			}
		} else {
			prevUpper = false
		}
		result = append(result, conv(r))
	}
	return string(result)
}

type fieldOpts struct {
	name                 string
	flagName             string
	shortFlagName        string
	usage                string
	envName              string
	envNameSetExplicitly bool
	configName           string
	defaultValue         reflect.Value
}

func parseField(val reflect.Value, field *reflect.StructField) *fieldOpts {
	o := &fieldOpts{
		name:         field.Name,
		defaultValue: val,
	}
	if flagName, ok := field.Tag.Lookup("flag"); ok {
		o.flagName = flagName
	}
	if o.flagName == "" {
		o.flagName = convertFieldName(field.Name, '-', unicode.ToLower)
	}
	if shorthand, ok := field.Tag.Lookup("short"); ok {
		o.shortFlagName = shorthand
	}
	if usage, ok := field.Tag.Lookup("usage"); ok {
		o.usage = usage
	}
	if envName, ok := field.Tag.Lookup("env"); ok {
		o.envName = envName
		o.envNameSetExplicitly = true
	}
	if o.envName == "" {
		o.envName = convertFieldName(field.Name, '_', unicode.ToUpper)
		o.envNameSetExplicitly = false
	}
	if configName, ok := field.Tag.Lookup("config"); ok {
		o.configName = configName
	}
	if o.configName == "" {
		o.configName = convertFieldName(field.Name, '_', unicode.ToLower)
	}
	return o
}

func registerFlag(flags *pflag.FlagSet, opts *fieldOpts) (*pflag.Flag, error) {
	switch val := opts.defaultValue.Interface().(type) {
	case int:
		flags.IntP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case int8:
		flags.Int8P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case int16:
		flags.Int16P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case int32:
		flags.Int32P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case int64:
		flags.Int64P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint:
		flags.UintP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint8:
		flags.Uint8P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint16:
		flags.Uint16P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint32:
		flags.Uint32P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint64:
		flags.Uint64P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case float32:
		flags.Float32P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case float64:
		flags.Float64P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case bool:
		flags.BoolP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case string:
		flags.StringP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case time.Duration:
		flags.DurationP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case net.IP:
		flags.IPP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case net.IPNet:
		flags.IPNetP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case net.IPMask:
		flags.IPMaskP(opts.flagName, opts.shortFlagName, val, opts.usage)

	case []int:
		flags.IntSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []int32:
		flags.Int32SliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []int64:
		flags.Int64SliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []uint:
		flags.UintSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []float32:
		flags.Float32SliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []float64:
		flags.Float64SliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []bool:
		flags.BoolSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []string:
		flags.StringSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []time.Duration:
		flags.DurationSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []net.IP:
		flags.IPSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	default:
		pflagVal, ok := getPflagValueIfImplemented(opts.defaultValue)
		if !ok {
			return nil, fmt.Errorf("unsupported field type: %T", val)
		}
		flags.VarP(pflagVal, opts.flagName, opts.shortFlagName, opts.usage)
	}
	return flags.Lookup(opts.flagName), nil
}

type Loader struct {
	LoadFromArgs bool
	Args         []string

	LoadFromEnv  bool
	EnvFilePaths []string
	EnvPrefix    string

	LoadFromConfigFile  bool
	ConfigPath          string
	ConfigType          string
	ConfigArg           string
	ConfigArgShorthand  string
	ConfigEnv           string
	ConfigEnvOmitPrefix bool

	vip *viper.Viper
}

// The null bytes should ensure that these don't conflict with any sensible tag
// names.
const (
	configPathViperKey  = "_\u0000_CONFIG_PATH_\u0000_"
	mapstructureTagName = "_\u0000_MAPSTRUCTURE_TAG_\u0000_"
)

func New() *Loader {
	return &Loader{
		LoadFromArgs: true,
		Args:         os.Args,

		LoadFromEnv:  true,
		EnvFilePaths: []string{".env"},

		LoadFromConfigFile: true,
		ConfigArg:          "config",
		ConfigEnv:          "CONFIG",
	}
}

func (l *Loader) Load(dst interface{}) error {
	dstVal := reflect.ValueOf(dst)
	if dstVal.Kind() != reflect.Ptr || dstVal.Elem().Kind() != reflect.Struct {
		return fatalErr(errors.New("dst must be a pointer to a struct"))
	}
	fieldOpts := l.parseFieldOpts(dstVal)
	l.vip = viper.New()
	l.setDefaults(fieldOpts)
	var errs *multierror.Error
	if l.LoadFromArgs {
		errs = multierror.Append(errs, l.loadFromArgs(fieldOpts))
	}
	if l.LoadFromEnv {
		errs = multierror.Append(errs, l.loadFromEnv(fieldOpts))
	}
	if l.LoadFromConfigFile {
		errs = multierror.Append(errs, l.loadFromConfigFile(fieldOpts))
	}
	err := l.vip.Unmarshal(dst, func(c *mapstructure.DecoderConfig) {
		c.TagName = mapstructureTagName
		c.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			optionDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)
	})
	return multierror.Append(errs, fatalErr(err)).ErrorOrNil()
}

func (l *Loader) parseFieldOpts(dstVal reflect.Value) map[string]*fieldOpts {
	dstVal = dstVal.Elem()
	dstTyp := dstVal.Type()
	fieldOpts := map[string]*fieldOpts{}
	for i := 0; i < dstTyp.NumField(); i++ {
		if field := dstTyp.Field(i); field.IsExported() {
			f := parseField(dstVal.Field(i), &field)
			fieldOpts[f.name] = f
		}
	}
	return fieldOpts
}

func (l *Loader) setDefaults(opts map[string]*fieldOpts) {
	for _, opt := range opts {
		l.vip.SetDefault(opt.name, opt.defaultValue.Interface())
	}
}

func (l *Loader) loadFromArgs(opts map[string]*fieldOpts) error {
	if len(l.Args) == 0 {
		return argsErr(errors.New("program name is missing from Args"))
	}
	flags := pflag.NewFlagSet(l.Args[0], pflag.ContinueOnError)
	for _, opt := range opts {
		flag, err := registerFlag(flags, opt)
		if err != nil {
			return argsErr(err)
		}
		l.vip.BindPFlag(opt.name, flag)
	}
	if l.LoadFromConfigFile && l.ConfigArg != "" && flags.Lookup(l.ConfigArg) == nil {
		flags.StringP(l.ConfigArg, l.ConfigArgShorthand, l.ConfigPath, "Config file path")
		l.vip.BindPFlag(configPathViperKey, flags.Lookup(l.ConfigArg))
	}
	return argsErr(flags.Parse(l.Args[1:]))
}

func (l *Loader) loadFromEnv(opts map[string]*fieldOpts) error {
	prefix := l.EnvPrefix
	if prefix != "" {
		prefix += "_"
	}
	for _, opt := range opts {
		if opt.envNameSetExplicitly {
			l.vip.BindEnv(opt.name, opt.envName)
		} else {
			l.vip.BindEnv(opt.name, prefix+opt.envName)
		}
	}
	if l.LoadFromConfigFile && l.ConfigEnv != "" {
		if l.ConfigEnvOmitPrefix {
			l.vip.BindEnv(configPathViperKey, l.ConfigEnv)
		} else {
			l.vip.BindEnv(configPathViperKey, prefix+l.ConfigEnv)
		}
	}
	l.vip.AllowEmptyEnv(true)
	var errs *multierror.Error
	if len(l.EnvFilePaths) > 0 {
		for _, path := range l.EnvFilePaths {
			errs = multierror.Append(errs, envErr(godotenv.Load(path)))
		}
	}
	return errs.ErrorOrNil()
}

func (l *Loader) loadFromConfigFile(opts map[string]*fieldOpts) error {
	configPath := l.vip.GetString(configPathViperKey)
	if configPath == "" {
		return nil
	}
	l.vip.SetConfigFile(configPath)
	l.vip.SetConfigType(l.ConfigType)
	for _, opt := range opts {
		l.vip.RegisterAlias(opt.configName, opt.name)
	}
	return fileErr(l.vip.ReadInConfig())
}

type FatalErr struct{ error }

func fatalErr(err error) error {
	if err == nil {
		return nil
	}
	return multierror.Append(FatalErr{multierror.Prefix(err, "load:")})
}

func (e FatalErr) Unwrap() error              { return errors.Unwrap(e.error) }
func (e FatalErr) Is(target error) bool       { return errors.Is(e.error, target) }
func (e FatalErr) As(target interface{}) bool { return errors.As(e.error, target) }

type ArgsErr struct{ error }

func argsErr(err error) error {
	if err == nil {
		return nil
	}
	return ArgsErr{multierror.Prefix(err, "arguments:")}
}

func (e ArgsErr) Unwrap() error              { return errors.Unwrap(e.error) }
func (e ArgsErr) Is(target error) bool       { return errors.Is(e.error, target) }
func (e ArgsErr) As(target interface{}) bool { return errors.As(e.error, target) }

type EnvErr struct{ error }

func envErr(err error) error {
	if err == nil {
		return nil
	}
	return EnvErr{multierror.Prefix(err, "env:")}
}

func (e EnvErr) Unwrap() error              { return errors.Unwrap(e.error) }
func (e EnvErr) Is(target error) bool       { return errors.Is(e.error, target) }
func (e EnvErr) As(target interface{}) bool { return errors.As(e.error, target) }

type FileErr struct{ error }

func fileErr(err error) error {
	if err == nil {
		return nil
	}
	return FileErr{multierror.Prefix(err, "config file:")}
}

func (e FileErr) Unwrap() error              { return errors.Unwrap(e.error) }
func (e FileErr) Is(target error) bool       { return errors.Is(e.error, target) }
func (e FileErr) As(target interface{}) bool { return errors.As(e.error, target) }
