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

// LoggerFor when set, returns the *slog.Logger that channel uses for events
// scoped to the given name. The name is the channel's LogicalName (e.g.
// "ctrl", "link", "agent"), so an embedding application can both route
// channel logging through its own slog setup and control verbosity per
// channel type independently of the global level.
//
// When nil, channel falls back to a default logger that forwards to
// pfxlog/logrus, preserving the logging behavior channel had before this seam
// existed. Applications that wire LoggerFor get accurate source attribution
// from slog's own caller capture; the pfxlog fallback attributes the call to
// the forwarding handler, which is a minor cosmetic difference on these
// events.
//
// Set this once during startup before any channels are created.
var LoggerFor func(name string) *slog.Logger

// channelLogger returns the logger for the given logical name, using LoggerFor
// when set and the pfxlog-backed default otherwise. Callers that have a
// per-channel Options.Logger should prefer (*channelImpl).eventLogger, which
// consults it first.
func channelLogger(name string) *slog.Logger {
	if f := LoggerFor; f != nil {
		return f(name)
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
