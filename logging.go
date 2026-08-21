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
	"os"
	"slices"
	"sync"
	"sync/atomic"
)

// LevelTrace is channel's trace log level, below slog.LevelDebug. It matches
// the trace level foundation's logging registry uses, so trace records emitted
// by channel gate and render consistently with the rest of the slog output.
const LevelTrace = slog.LevelDebug - 4

// loggerFor holds the installed resolver in an atomic.Pointer so getLoggerFor
// stays lock-free: For consults it on the message paths, where a shared mutex
// would serialize every channel's rx and tx goroutines (the same reason the
// area-logger cache is a sync.Map).
var loggerFor atomic.Pointer[func(name string) *slog.Logger]

// SetLoggerFor installs the resolver channel routes its logging through, letting
// an embedding application supply its own slog logger and control verbosity per
// name. It governs both a channel's lifecycle-event logger (keyed by the
// channel's LogicalName, e.g. "ctrl", "link", "agent") and channel's internal
// package loggers (keyed by area name, e.g. "channel.dialer"), so an embedder
// controls channel's logging across the board, not just its events. When no
// resolver is installed, channel falls back to loggers backed by slog.Default().
//
// Set this once during startup, before any channels are created.
func SetLoggerFor(f func(name string) *slog.Logger) {
	loggerFor.Store(&f)
}

// getLoggerFor returns the installed resolver, or nil if none has been set.
func getLoggerFor() func(name string) *slog.Logger {
	if p := loggerFor.Load(); p != nil {
		return *p
	}
	return nil
}

// resolveEventLogger picks the logger for a channel event by precedence: the
// per-channel Options.Logger, then the supplied loggerFor resolver keyed by name,
// then the slog.Default()-backed default. It always returns a non-nil logger: a
// resolver that returns nil for a name falls through to the default, so the
// channel never caches a nil logger and panics on its first event. Taking
// loggerFor as a parameter keeps the precedence logic free of package state so it
// can be exercised directly.
func resolveEventLogger(opts *Options, name string, loggerFor func(name string) *slog.Logger) *slog.Logger {
	if opts != nil && opts.Logger != nil {
		return opts.Logger
	}
	return resolveArea(name, loggerFor)
}

// resolveArea returns loggerFor(name) when the resolver yields a logger, else
// the cached slog.Default()-backed area logger. Shared by For (global resolver)
// and resolveEventLogger (explicitly-supplied resolver) so both honor the same
// fallback. It always returns a non-nil logger.
func resolveArea(name string, loggerFor func(name string) *slog.Logger) *slog.Logger {
	if loggerFor != nil {
		if logger := loggerFor(name); logger != nil {
			return logger
		}
	}
	return defaultAreaLogger(name)
}

// For returns the channel-scoped *slog.Logger for a logical area name (e.g.
// "channel.dialer"), used by channel's internal package logging. It routes
// through the installed SetLoggerFor resolver, so an embedding application's
// logger and per-name level control govern channel's internal logs, not just
// its lifecycle events. When no resolver is installed it returns a cached
// slog.Default()-backed logger with "channel"=name bound.
func For(name string) *slog.Logger {
	return resolveArea(name, getLoggerFor())
}

// defaultLoggerByName caches the slog.Default()-backed area loggers by name.
// Area names are a small fixed set, so this only ever grows to that size. It is
// a sync.Map rather than a mutex-guarded map because callers reach these loggers
// from the message paths, where a shared lock would serialize every channel's rx
// and tx goroutines against a cache that never misses after startup.
var defaultLoggerByName sync.Map

// defaultAreaLogger returns (and caches) the slog.Default()-backed logger for an
// area name, with "channel"=name bound. Used only when no SetLoggerFor resolver
// is installed.
func defaultAreaLogger(name string) *slog.Logger {
	if logger, ok := defaultLoggerByName.Load(name); ok {
		return logger.(*slog.Logger)
	}
	logger, _ := defaultLoggerByName.LoadOrStore(name, slog.New(&defaultHandler{}).With(slog.String("channel", name)))
	return logger.(*slog.Logger)
}

// fallbackHandler receives records when the process slog default resolves back
// into one of channel's own delegating handlers. It writes to stderr, the same
// place slog's own default goes.
var fallbackHandler slog.Handler = slog.NewTextHandler(os.Stderr, nil)

// defaultHandler forwards records to the process slog.Default() handler,
// resolved at the time each record is handled, so channel's fallback logging
// tracks a slog default installed after channel's loggers are created. WithAttrs
// and WithGroup are recorded as an ordered op list and replayed onto the live
// default per record, preserving the interleaving between them: For is exported,
// so a caller that uses .WithGroup on it must get correct nesting rather than
// silently flattened output.
type defaultHandler struct {
	ops []handlerOp
}

// handlerOp records one WithAttrs call (attrs set) or one WithGroup call (group
// set), so resolve can replay them in the order they were applied.
type handlerOp struct {
	attrs []slog.Attr
	group string
}

// resolve replays the recorded ops onto the live slog.Default() handler.
func (h *defaultHandler) resolve() slog.Handler {
	return h.replay(nonDelegating(slog.Default().Handler()))
}

// replay applies the recorded ops, in order, onto base. Taking base as a
// parameter keeps the op replay free of package state so it can be exercised
// directly.
func (h *defaultHandler) replay(base slog.Handler) slog.Handler {
	handler := base
	for _, op := range h.ops {
		if op.group != "" {
			handler = handler.WithGroup(op.group)
		} else {
			handler = handler.WithAttrs(op.attrs)
		}
	}
	return handler
}

// nonDelegating returns target, or the fallback if target is one of channel's own
// delegating handlers. slog.SetDefault(channel.For("app")) is a reasonable-looking
// way to route an application through channel's logger, and it makes the default's
// handler the very handler that delegates to the default, so forwarding blindly
// recurses until the stack overflows. A default that wraps one of these handlers
// rather than being one is not detectable here and still recurses. Taking the
// handler as a parameter keeps this decision free of package state so it can be
// exercised directly.
func nonDelegating(target slog.Handler) slog.Handler {
	if _, delegating := target.(*defaultHandler); delegating {
		return fallbackHandler
	}
	return target
}

func (h *defaultHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.resolve().Enabled(ctx, level)
}

func (h *defaultHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.resolve().Handle(ctx, r)
}

func (h *defaultHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return &defaultHandler{ops: append(slices.Clone(h.ops), handlerOp{attrs: slices.Clone(attrs)})}
}

func (h *defaultHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &defaultHandler{ops: append(slices.Clone(h.ops), handlerOp{group: name})}
}
