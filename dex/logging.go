// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package dex

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/decred/slog"
	"github.com/jrick/logrotate/rotator"
)

// Disabled is a Logger that will never output anything.
var Disabled Logger = &logger{
	Logger:  slog.Disabled,
	level:   LevelOff,
	backend: slog.NewBackend(io.Discard),
}

// Level constants.
const (
	LevelTrace    = slog.LevelTrace
	LevelDebug    = slog.LevelDebug
	LevelInfo     = slog.LevelInfo
	LevelWarn     = slog.LevelWarn
	LevelError    = slog.LevelError
	LevelCritical = slog.LevelCritical
	LevelOff      = slog.LevelOff

	DefaultLogLevel = LevelInfo
)

// Logger is a logger. Many dcrdex types will take a logger as an argument.
type Logger interface {
	slog.Logger
	SubLogger(name string) Logger
	FileLogger(r *rotator.Rotator) Logger
	Meter(callerID string, delay time.Duration) Logger
}

// LoggerMaker allows creation of new log subsystems with predefined levels.
type LoggerMaker struct {
	*slog.Backend
	DefaultLevel slog.Level
	Levels       map[string]slog.Level
}

// logger contains the slog.Logger and fields needed to spawn subloggers. It
// satisfies the Logger interface.
type logger struct {
	slog.Logger
	name    string
	level   slog.Level
	levels  map[string]slog.Level
	backend *slog.Backend

	meterMtx sync.Mutex
	meters   map[string]time.Time
}

// SubLogger creates a new Logger for the subsystem with the given name. If name
// exists in the levels map, use that level, otherwise the parent's log level is
// used.
func (lggr *logger) SubLogger(name string) Logger {
	return lggr.newLoggerWithBackend(lggr.backend, name)
}

// FileLogger creates a logger that logs to a file rotator. Subloggers will also
// log to the file only.
func (lggr *logger) FileLogger(r *rotator.Rotator) Logger {
	return lggr.newLoggerWithBackend(slog.NewBackend(r), "F")
}

func (lggr *logger) newLoggerWithBackend(backend *slog.Backend, name string) *logger {
	level := lggr.level
	// If name is in the levels map, use that level.
	if lvl, ok := lggr.levels[name]; ok {
		level = lvl
	}

	combinedName := fmt.Sprintf("%s[%s]", lggr.name, name)
	newLggr := backend.Logger(combinedName)
	newLggr.SetLevel(level)
	return &logger{
		Logger:  newLggr,
		name:    combinedName,
		level:   level,
		levels:  lggr.levels,
		backend: backend,
	}
}

// Meter enforces a time delay on logging. The first call to a metered logger
// always logs. Subsequent calls for the same callerID are ignored until the
// delay is surpassed.
func (log *logger) Meter(callerID string, delay time.Duration) Logger {
	log.meterMtx.Lock()
	defer log.meterMtx.Unlock()
	if log.meters == nil {
		log.meters = make(map[string]time.Time)
	}
	if lastLog, exists := log.meters[callerID]; exists && time.Since(lastLog) < delay {
		return Disabled
	}
	log.meters[callerID] = time.Now()
	return log
}

// LogRotator creates a file logger that rotates up to 8 files of 32 MiB each.
func LogRotator(dir, name string) (*rotator.Rotator, error) {
	const maxLogRolls = 8
	if err := os.MkdirAll(dir, 0744); err != nil {
		return nil, fmt.Errorf("error creating log directory: %w", err)
	}

	logFilename := filepath.Join(dir, name)
	return rotator.New(logFilename, 32*1024, false, maxLogRolls)
}

func inUTC() slog.BackendOption {
	return slog.WithFlags(slog.LUTC)
}

// NewLogger creates a new Logger with the given name, log level, and io.Writer.
func NewLogger(name string, lvl slog.Level, writer io.Writer, utc ...bool) Logger {
	var opts []slog.BackendOption
	if len(utc) > 0 && utc[0] {
		opts = append(opts, inUTC())
	}
	backend := slog.NewBackend(writer, opts...)
	lggr := backend.Logger(name)
	lggr.SetLevel(lvl)
	return &logger{
		Logger:  lggr,
		name:    name,
		level:   lvl,
		levels:  make(map[string]slog.Level),
		backend: backend,
	}
}

// StdOutLogger creates a Logger with the provided name with lvl as the log
// level and prints to standard out.
func StdOutLogger(name string, lvl slog.Level, utc ...bool) Logger {
	var opts []slog.BackendOption
	if len(utc) > 0 && utc[0] {
		opts = append(opts, inUTC())
	}
	backend := slog.NewBackend(os.Stdout, opts...)
	lggr := backend.Logger(name)
	lggr.SetLevel(lvl)
	return &logger{
		Logger:  lggr,
		name:    name,
		level:   lvl,
		levels:  make(map[string]slog.Level),
		backend: backend,
	}
}

// NewLoggerMaker creates a new LoggerMaker from the provided io.Writer and
// debug level string. See SetLevels for details on the debug level string.
func NewLoggerMaker(writer io.Writer, debugLevel string, utc ...bool) (*LoggerMaker, error) {
	var opts []slog.BackendOption
	if len(utc) > 0 && utc[0] {
		opts = append(opts, inUTC())
	}
	lm := &LoggerMaker{
		Backend:      slog.NewBackend(writer, opts...),
		Levels:       make(map[string]slog.Level),
		DefaultLevel: DefaultLogLevel,
	}

	err := lm.SetLevels(debugLevel)
	if err != nil {
		return nil, err
	}

	return lm, nil
}

// SetLevelsFromMap sets all logs for certain subsystems with the same name to
// the corresponding log level in the map.
func (lm *LoggerMaker) SetLevelsFromMap(lvls map[string]slog.Level) {
	maps.Copy(lm.Levels, lvls)
}

// SetLevels either set the DefaultLevel or resets the Levels map for future
// subsystems created with the LoggerMaker.
//
// The debugLevel string can specify a single verbosity for the entire system:
// "trace", "debug", "info", "warn", "error", "critical", "off". The Levels map
// is not modified with this syntax.
//
// Or the verbosity can be specified for individual subsystems, separating
// subsystems by commas and assigning each specifically. Such a debugLevel
// string might look like `CORE=debug,SWAP=trace`. The DefaultLevel is not
// modified with this syntax.
func (lm *LoggerMaker) SetLevels(debugLevel string) error {
	// When the specified string doesn't have any delimiters, treat it as
	// the log level for all subsystems.
	if !strings.Contains(debugLevel, ",") && !strings.Contains(debugLevel, "=") {
		// Validate debug log level.
		lvl, ok := slog.LevelFromString(debugLevel)
		if !ok {
			str := "The specified debug level [%v] is invalid"
			return fmt.Errorf(str, debugLevel)
		}
		lm.DefaultLevel = lvl
		return nil
	}

	// Split the specified string into subsystem/level pairs while detecting
	// issues and update the log levels accordingly.
	levelPairs := strings.Split(debugLevel, ",")
	lm.Levels = make(map[string]slog.Level, len(levelPairs))
	for _, logLevelPair := range levelPairs {
		if !strings.Contains(logLevelPair, "=") {
			// Mixed defaults and key=val pairs are not permitted.
			str := "The specified debug level contains an invalid " +
				"subsystem/level pair [%v]"
			return fmt.Errorf(str, logLevelPair)
		}

		// Extract the specified subsystem and log level.
		fields := strings.Split(logLevelPair, "=")
		subsysID, logLevel := fields[0], fields[1]

		// Validate log level.
		lvl, ok := slog.LevelFromString(logLevel)
		if !ok {
			str := "The specified debug level [%v] is invalid"
			return fmt.Errorf(str, logLevel)
		}
		lm.Levels[subsysID] = lvl
	}
	return nil
}

// Level returns the log level for the named subsystem. If a level is not
// configured for this subsystem, the LoggerMaker's DefaultLevel is returned.
func (lm *LoggerMaker) Level(name string) slog.Level {
	level, found := lm.Levels[name]
	if found {
		return level
	}
	return lm.DefaultLevel
}

// NewLogger creates a new Logger for the subsystem with the given name. If a
// log level is specified, it is used for the Logger. Otherwise the DefaultLevel
// is used.
func (lm *LoggerMaker) NewLogger(name string, level ...slog.Level) Logger {
	lvl := lm.DefaultLevel
	if len(level) > 0 {
		lvl = level[0]
	}
	lggr := lm.Backend.Logger(name)
	lggr.SetLevel(lvl)
	return &logger{
		Logger:  lggr,
		name:    name,
		level:   lvl,
		levels:  lm.Levels,
		backend: lm.Backend,
	}
}

// Logger creates a logger with the provided name, using the log level for that
// name if it was set, otherwise the default log level. This differs from
// NewLogger, which does not look in the Level map for the name.
func (lm *LoggerMaker) Logger(name string) Logger {
	lggr := lm.Backend.Logger(name)
	lvl := lm.bestLevel(name)
	lggr.SetLevel(lvl)
	return &logger{
		Logger:  lggr,
		name:    name,
		level:   lvl,
		levels:  lm.Levels,
		backend: lm.Backend,
	}
}

// bestLevel takes a hierarchical list of logger names, least important to most
// important, and returns the best log level found in the Levels map, else the
// default.
func (lm *LoggerMaker) bestLevel(lvls ...string) slog.Level {
	lvl := lm.DefaultLevel
	for _, l := range lvls {
		lev, found := lm.Levels[l]
		if found {
			lvl = lev
		}
	}
	return lvl
}
