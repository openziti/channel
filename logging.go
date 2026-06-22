/*
	Copyright NetFoundry Inc.

	Licensed under the Apache License, Version 2.0 (the "License");
	you may not use this file except in compliance with the License.
	You may obtain a copy of the License at

	https://www.apache.org/licenses/LICENSE-2.0

	Unless required by applicable law or agreed to in writing, software
	distributed under the License is distributed on an "AS IS" BASIS,
	WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
	See the License for the specific language governing permissions and
	limitations under the License.
*/

package channel

import (
	"context"
	"log/slog"
	"sync"

	"github.com/michaelquigley/pfxlog"
	"github.com/sirupsen/logrus"
)

var (
	loggerForMu sync.Mutex
	loggerFor   func(name string) *slog.Logger
)

// SetLoggerFor installs the resolver that channel uses for events scoped to a
// channel's LogicalName (e.g. "ctrl", "link", "agent"), letting an embedding
// application route channel logging through its own slog setup and control
// verbosity per channel type. When no resolver is installed, channel falls back
// to a default logger that forwards to pfxlog/logrus, preserving the logging
// behavior channel had before this seam existed.
//
// Set this once during startup, before any channels are created. Each channel
// resolves and caches its logger at creation, so installing a resolver after
// channels already exist applies only to channels created afterward.
func SetLoggerFor(f func(name string) *slog.Logger) {
	loggerForMu.Lock()
	defer loggerForMu.Unlock()
	loggerFor = f
}

// getLoggerFor returns the installed resolver, or nil if none has been set.
func getLoggerFor() func(name string) *slog.Logger {
	loggerForMu.Lock()
	defer loggerForMu.Unlock()
	return loggerFor
}

// resolveEventLogger picks the logger for a channel event by precedence: the
// per-channel Options.Logger, then the supplied loggerFor resolver keyed by name,
// then the pfxlog-backed default. Taking loggerFor as a parameter keeps the
// precedence logic free of package state so it can be exercised directly.
func resolveEventLogger(opts *Options, name string, loggerFor func(name string) *slog.Logger) *slog.Logger {
	if opts != nil && opts.Logger != nil {
		return opts.Logger
	}
	if loggerFor != nil {
		return loggerFor(name)
	}
	return defaultChannelLogger(name)
}

var (
	defaultLoggerMu     sync.Mutex
	defaultLoggerByName = map[string]*slog.Logger{}
)

// defaultChannelLogger returns (and caches) the pfxlog-backed slog.Logger for
// a logical name. The name is bound as the "channel" attr so the channel type
// is visible in output even when no LoggerFor is wired, matching the attr the
// slog registry would bind.
func defaultChannelLogger(name string) *slog.Logger {
	defaultLoggerMu.Lock()
	defer defaultLoggerMu.Unlock()
	if logger, ok := defaultLoggerByName[name]; ok {
		return logger
	}
	logger := slog.New(&pfxlogHandler{}).With(slog.String("channel", name))
	defaultLoggerByName[name] = logger
	return logger
}

// pfxlogHandler is the default slog.Handler used when LoggerFor is unset. It
// forwards records to pfxlog/logrus so channel logging lands wherever the
// embedding application has already pointed logrus, unchanged from before the
// LoggerFor seam.
type pfxlogHandler struct {
	attrs []slog.Attr
}

func (h *pfxlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return logrus.StandardLogger().IsLevelEnabled(slogToLogrusLevel(level))
}

func (h *pfxlogHandler) Handle(_ context.Context, r slog.Record) error {
	entry := pfxlog.Logger().Entry
	if len(h.attrs) > 0 {
		entry = entry.WithFields(attrsToFields(h.attrs))
	}
	if r.NumAttrs() > 0 {
		fields := make(logrus.Fields, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			fields[a.Key] = a.Value.Any()
			return true
		})
		entry = entry.WithFields(fields)
	}
	entry.Log(slogToLogrusLevel(r.Level), r.Message)
	return nil
}

func (h *pfxlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	combined := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	combined = append(combined, h.attrs...)
	combined = append(combined, attrs...)
	return &pfxlogHandler{attrs: combined}
}

// WithGroup is a no-op; channel does not use slog groups for its events.
func (h *pfxlogHandler) WithGroup(string) slog.Handler {
	return h
}

func attrsToFields(attrs []slog.Attr) logrus.Fields {
	fields := make(logrus.Fields, len(attrs))
	for _, a := range attrs {
		fields[a.Key] = a.Value.Any()
	}
	return fields
}

// slogToLogrusLevel maps a slog.Level onto the nearest logrus level for the
// pfxlog fallback path.
func slogToLogrusLevel(l slog.Level) logrus.Level {
	switch {
	case l <= slog.LevelDebug-4:
		return logrus.TraceLevel
	case l <= slog.LevelDebug:
		return logrus.DebugLevel
	case l <= slog.LevelInfo:
		return logrus.InfoLevel
	case l <= slog.LevelWarn:
		return logrus.WarnLevel
	case l <= slog.LevelError:
		return logrus.ErrorLevel
	default:
		return logrus.ErrorLevel
	}
}
