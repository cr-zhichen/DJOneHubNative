package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logMu sync.RWMutex
	Log   = zap.NewNop()
	Sugar = Log.Sugar()
)

func ZapLogger() *zap.Logger {
	logMu.RLock()
	defer logMu.RUnlock()
	return Log
}

func SugarLogger() *zap.SugaredLogger {
	logMu.RLock()
	defer logMu.RUnlock()
	return Sugar
}

var readerIMSIRegistry = struct {
	mu sync.RWMutex
	m  map[string]string
}{
	m: make(map[string]string),
}

// LookupIMSIByReader 根据 reader 查找绑定的 IMSI。
func LookupIMSIByReader(reader string) (string, bool) {
	reader = strings.TrimSpace(reader)
	if reader == "" {
		return "", false
	}
	readerIMSIRegistry.mu.RLock()
	imsi, ok := readerIMSIRegistry.m[reader]
	readerIMSIRegistry.mu.RUnlock()
	if !ok {
		return "", false
	}
	imsi = strings.TrimSpace(imsi)
	if imsi == "" {
		return "", false
	}
	return imsi, true
}

// fixedWidthColorLevelEncoder 固定宽度（5字符）的彩色日志等级编码器
func fixedWidthColorLevelEncoder(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	s := level.CapitalString()
	for len(s) < 5 {
		s += " "
	}
	switch level {
	case zapcore.DebugLevel:
		s = "\x1b[35m" + s + "\x1b[0m"
	case zapcore.InfoLevel:
		s = "\x1b[34m" + s + "\x1b[0m"
	case zapcore.WarnLevel:
		s = "\x1b[33m" + s + "\x1b[0m"
	case zapcore.ErrorLevel:
		s = "\x1b[31m" + s + "\x1b[0m"
	case zapcore.FatalLevel, zapcore.PanicLevel, zapcore.DPanicLevel:
		s = "\x1b[31;1m" + s + "\x1b[0m"
	}
	enc.AppendString(s)
}

// fixedWidthLevelEncoder 固定宽度（5字符）的日志等级编码器（无颜色，用于文件）
func fixedWidthLevelEncoder(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	s := level.CapitalString()
	for len(s) < 5 {
		s += " "
	}
	enc.AppendString(s)
}

type LogConfig struct {
	Debug    bool
	Filename string // 主日志软链名称（如 logs/app.log）
	MaxAge   int    // 保留天数，默认 30 天
	// 以下字段为了向后兼容暂时保留，但不再起作用
	MaxSize    int
	MaxBackups int
	Compress   bool
}

type devicePrefixCore struct {
	zapcore.Core
	fields []zapcore.Field
}

func (c *devicePrefixCore) With(fields []zapcore.Field) zapcore.Core {
	merged := make([]zapcore.Field, 0, len(c.fields)+len(fields))
	merged = append(merged, c.fields...)
	merged = append(merged, fields...)
	return &devicePrefixCore{
		Core:   c.Core.With(filterDeviceFields(fields)),
		fields: merged,
	}
}

func (c *devicePrefixCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !c.Enabled(ent.Level) {
		return ce
	}
	return ce.AddCore(ent, c)
}

func (c *devicePrefixCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	if deviceID, ok := resolveDeviceID(c.fields, fields); ok {
		prefix := "[" + deviceID + "] "
		if !strings.HasPrefix(ent.Message, prefix) {
			ent.Message = prefix + ent.Message
		}
	}
	return c.Core.Write(ent, filterDeviceFields(fields))
}

func resolveDeviceID(contextFields, callFields []zapcore.Field) (string, bool) {
	if v, ok := extractDeviceID(callFields); ok {
		return v, true
	}
	if v, ok := extractDeviceID(contextFields); ok {
		return v, true
	}
	if reader, ok := extractReader(callFields); ok {
		if device, ok := LookupIMSIByReader(reader); ok {
			return device, true
		}
	}
	if reader, ok := extractReader(contextFields); ok {
		if device, ok := LookupIMSIByReader(reader); ok {
			return device, true
		}
	}
	return "", false
}

func extractDeviceID(fields []zapcore.Field) (string, bool) {
	for _, field := range fields {
		if field.Key != "device" && field.Key != "device_id" {
			continue
		}
		enc := zapcore.NewMapObjectEncoder()
		field.AddTo(enc)
		if v, ok := enc.Fields[field.Key].(string); ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return v, true
			}
		}
	}
	return "", false
}

func extractReader(fields []zapcore.Field) (string, bool) {
	for _, field := range fields {
		if field.Key != "reader" {
			continue
		}
		enc := zapcore.NewMapObjectEncoder()
		field.AddTo(enc)
		if v, ok := enc.Fields[field.Key].(string); ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return v, true
			}
		}
	}
	return "", false
}

func filterDeviceFields(fields []zapcore.Field) []zapcore.Field {
	if len(fields) == 0 {
		return fields
	}
	out := make([]zapcore.Field, 0, len(fields))
	for _, field := range fields {
		if field.Key == "device" || field.Key == "device_id" {
			continue
		}
		out = append(out, field)
	}
	return out
}

func Setup(cfg LogConfig) {
	consoleEncoderConfig := zap.NewDevelopmentEncoderConfig()
	consoleEncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("[2006-01-02 15:04:05]")
	consoleEncoderConfig.EncodeLevel = fixedWidthColorLevelEncoder
	consoleEncoderConfig.EncodeCaller = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		const width = 28
		s := caller.TrimmedPath()
		if len(s) < width {
			s += strings.Repeat(" ", width-len(s))
		}
		enc.AppendString(s)
	}
	consoleEncoderConfig.ConsoleSeparator = " "

	fileEncoderConfig := zap.NewDevelopmentEncoderConfig()
	fileEncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("[2006-01-02 15:04:05]")
	fileEncoderConfig.EncodeLevel = fixedWidthLevelEncoder
	fileEncoderConfig.EncodeCaller = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
		const width = 28
		s := caller.TrimmedPath()
		if len(s) < width {
			s += strings.Repeat(" ", width-len(s))
		}
		enc.AppendString(s)
	}
	fileEncoderConfig.ConsoleSeparator = " "

	// 默认配置
	if cfg.Filename == "" {
		cfg.Filename = "logs/app.log"
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 30
	}

	// 确保日志目录存在
	_ = os.MkdirAll(filepath.Dir(cfg.Filename), 0755)

	// 提取后缀来生成如 logs/app-%Y-%m-%d.log 的模式
	ext := filepath.Ext(cfg.Filename) // 比如 .log
	base := strings.TrimSuffix(cfg.Filename, ext)
	logPattern := base + "-%Y-%m-%d" + ext

	// 文件输出 (使用 file-rotatelogs 按天进行轮转)
	rl, err := rotatelogs.New(
		logPattern,
		rotatelogs.WithLinkName(cfg.Filename), // 维持软链（如 logs/app.log）
		rotatelogs.WithMaxAge(time.Duration(cfg.MaxAge)*24*time.Hour),
		rotatelogs.WithRotationTime(24*time.Hour), // 每天切割
	)

	var fileWriter zapcore.WriteSyncer
	if err != nil {
		// 降级到普通的 stdout 控制台如果初始化 rotatelogs 失败
		fileWriter = zapcore.AddSync(os.Stdout)
	} else {
		fileWriter = zapcore.AddSync(rl)
	}

	// 控制台输出
	consoleWriter := zapcore.AddSync(os.Stdout)

	level := getLogLevel(cfg.Debug)
	consoleCore := zapcore.NewCore(zapcore.NewConsoleEncoder(consoleEncoderConfig), consoleWriter, level)
	fileCore := zapcore.NewCore(zapcore.NewConsoleEncoder(fileEncoderConfig), fileWriter, level)

	// SSE 日志推送核心（用于前端实时日志）
	sseCore := NewSSECore(GlobalBroadcaster, level)

	core := &devicePrefixCore{Core: zapcore.NewTee(consoleCore, fileCore, sseCore)}

	log := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	sugar := log.Sugar()
	logMu.Lock()
	Log = log
	Sugar = sugar
	logMu.Unlock()
}

func getLogLevel(debug bool) zapcore.LevelEnabler {
	if debug {
		return zap.DebugLevel
	}
	return zap.InfoLevel
}

func Info(msg string, args ...interface{}) {
	SugarLogger().Infow(msg, args...)
}

func Error(msg string, args ...interface{}) {
	SugarLogger().Errorw(msg, args...)
}

func Debug(msg string, args ...interface{}) {
	SugarLogger().Debugw(msg, args...)
}

// RunDebug 仅在 go run 场景下输出 Debug 日志。
func RunDebug(msg string, args ...interface{}) {
	if IsGoRun() {
		SugarLogger().Debugw(msg, args...)
	}
}

// RunWarn 仅在 go run 场景下输出 Warn 日志。
func RunWarn(msg string, args ...interface{}) {
	if IsGoRun() {
		SugarLogger().Warnw(msg, args...)
	}
}

func Warn(msg string, args ...interface{}) {
	SugarLogger().Warnw(msg, args...)
}
