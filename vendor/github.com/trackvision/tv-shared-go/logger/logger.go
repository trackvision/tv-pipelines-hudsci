package logger

import (
	"os"
	"runtime"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var zapLog *zap.Logger

func encodeLevel() zapcore.LevelEncoder {
	return func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
		switch l {
		case zapcore.DebugLevel:
			enc.AppendString("DEBUG")
		case zapcore.InfoLevel:
			enc.AppendString("INFO")
		case zapcore.WarnLevel:
			enc.AppendString("WARNING")
		case zapcore.ErrorLevel:
			enc.AppendString("ERROR")
		case zapcore.DPanicLevel:
			enc.AppendString("CRITICAL")
		case zapcore.PanicLevel:
			enc.AppendString("ALERT")
		case zapcore.FatalLevel:
			enc.AppendString("EMERGENCY")
		}
	}
}

func init() {
	// TODO following config is optimised for GCP (https://github.com/uber-go/zap/issues/1095), make it configurable for other clouds
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.LevelKey = "severity"
	encoderConfig.EncodeLevel = encodeLevel()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	encoderConfig.EncodeLevel = encodeLevel()
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.Lock(os.Stdout),
		zap.NewAtomicLevelAt(zapcore.InfoLevel),
	)

	zapLog = zap.New(core)
}

func Trace() {
	pc, file, line, ok := runtime.Caller(1)
	if !ok {
		Info("Trace", zap.String("file", "unknown"), zap.Int("line", 0), zap.String("function", "unknown"))
		return
	}

	fn := runtime.FuncForPC(pc)
	if fn == nil {
		Info("Trace", zap.String("file", "unknown"), zap.Int("line", 0), zap.String("function", "unknown"))
		return
	}

	Info("Trace", zap.String("file", file), zap.Int("line", line), zap.String("function", fn.Name()))

}

func Info(message string, fields ...zap.Field) {
	zapLog.Info(message, fields...)
}

func Debug(message string, fields ...zap.Field) {
	zapLog.Debug(message, fields...)
}

func Warn(message string, fields ...zap.Field) {
	zapLog.Warn(message, fields...)
}

func Error(message string, fields ...zap.Field) {
	zapLog.Error(message, fields...)
}

func Fatal(message string, fields ...zap.Field) {
	zapLog.Fatal(message, fields...)
}
