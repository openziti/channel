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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventLoggerResolution verifies the lifecycle-logger precedence:
// Options.Logger first, then the installed LoggerFor resolver, then a non-nil
// default. It exercises the pure resolver with injected funcs rather than
// mutating package state, so it never races background channel goroutines.
func TestEventLoggerResolution(t *testing.T) {
	optLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	globalLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	loggerFor := func(string) *slog.Logger { return globalLogger }

	t.Run("Options.Logger wins over LoggerFor", func(t *testing.T) {
		got := resolveEventLogger(&Options{Logger: optLogger}, "link", loggerFor)
		assert.Same(t, optLogger, got)
	})

	t.Run("LoggerFor used when Options.Logger nil", func(t *testing.T) {
		var gotName string
		named := func(name string) *slog.Logger {
			gotName = name
			return globalLogger
		}
		got := resolveEventLogger(&Options{}, "ctrl", named)
		assert.Same(t, globalLogger, got)
		assert.Equal(t, "ctrl", gotName, "LoggerFor should be keyed by the channel's logical name")
	})

	t.Run("default used when neither set", func(t *testing.T) {
		assert.NotNil(t, resolveEventLogger(&Options{}, "agent", nil))
	})

	t.Run("nil options falls back to LoggerFor", func(t *testing.T) {
		assert.Same(t, globalLogger, resolveEventLogger(nil, "agent", loggerFor))
	})

	t.Run("resolver returning nil falls back to default", func(t *testing.T) {
		nilResolver := func(string) *slog.Logger { return nil }
		assert.NotNil(t, resolveEventLogger(&Options{}, "agent", nilResolver),
			"a resolver returning nil must not be cached; fall back to the default")
	})
}

// TestDelegationTarget covers the choice channel's default handler makes about
// where to forward, which is what keeps it from delegating to itself.
func TestDelegationTarget(t *testing.T) {
	t.Run("forwards to an unrelated default", func(t *testing.T) {
		other := slog.NewTextHandler(io.Discard, nil)
		assert.Same(t, other, nonDelegating(other))
	})

	t.Run("falls back when the default is one of ours", func(t *testing.T) {
		assert.Same(t, fallbackHandler, nonDelegating(&defaultHandler{}),
			"forwarding to another delegating handler recurses until the stack overflows")
	})

	t.Run("falls back when the default is ours with attrs bound", func(t *testing.T) {
		withAttrs := (&defaultHandler{}).WithAttrs([]slog.Attr{slog.String("channel", "app")})
		assert.Same(t, fallbackHandler, nonDelegating(withAttrs),
			"With() returns a new delegating handler, which recurses the same way")
	})
}

// TestDefaultHandlerReplaysGroups verifies the fallback default handler honors
// WithGroup and preserves attr/group interleaving: For is exported, so a caller
// that groups attrs must get nested output rather than silently flattened. The
// ops are replayed onto a local base handler so the test needs no global
// slog.Default() and cannot race background channel goroutines.
func TestDefaultHandlerReplaysGroups(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)

	// The op chain a caller of For would build: channel=name bound by For, an attr
	// added before a group, then a group opened.
	var h slog.Handler = &defaultHandler{}
	h = h.WithAttrs([]slog.Attr{slog.String("channel", "channel.test")})
	h = h.WithAttrs([]slog.Attr{slog.Int("a", 1)})
	h = h.WithGroup("g")

	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	rec.AddAttrs(slog.Int("b", 2))
	require.NoError(t, h.(*defaultHandler).replay(base).Handle(context.Background(), rec))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "channel.test", got["channel"], "attr bound before the group stays at the root")
	assert.EqualValues(t, 1, got["a"], "attr bound before the group stays at the root")
	group, ok := got["g"].(map[string]any)
	require.True(t, ok, "WithGroup must nest attrs added after it under g, not flatten them")
	assert.EqualValues(t, 2, group["b"], "the record attr added after WithGroup nests under g")
}

// TestDefaultHandlerReplaysAttrsBeforeEnabled verifies that enablement is
// evaluated on the same bound handler that will handle the record.
func TestDefaultHandlerReplaysAttrsBeforeEnabled(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	slog.SetDefault(slog.New(&attrEnabledHandler{}))

	h := (&defaultHandler{}).WithAttrs([]slog.Attr{slog.String("channel", "channel.test")})
	assert.True(t, h.Enabled(context.Background(), slog.LevelDebug),
		"the process handler enables debug only after the channel attr is bound")
}

type attrEnabledHandler struct {
	enabled bool
}

func (h *attrEnabledHandler) Enabled(context.Context, slog.Level) bool {
	return h.enabled
}

func (h *attrEnabledHandler) Handle(context.Context, slog.Record) error {
	return nil
}

func (h *attrEnabledHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	for _, attr := range attrs {
		if attr.Key == "channel" && attr.Value.String() == "channel.test" {
			next.enabled = true
		}
	}
	return &next
}

func (h *attrEnabledHandler) WithGroup(string) slog.Handler {
	return h
}

// TestForInstalledAsSlogDefault logs through channel with a channel logger
// installed as the process slog default, which recursed until the stack
// overflowed before the delegation check existed.
//
// It runs in a subprocess for two reasons. A stack overflow is fatal, so an
// in-process test cannot report it. And slog.SetDefault also redirects the
// standard log package into the slog default, so a test cannot put the default
// back afterwards: the default it captured writes through the log package, and
// restoring it makes the two feed each other and hang. Nothing to restore in a
// process that is about to exit.
func TestForInstalledAsSlogDefault(t *testing.T) {
	if os.Getenv(slogDefaultProbeVar) == "1" {
		slog.SetDefault(For("app"))

		For("channel.impl").Error("logged while channel's own logger is the slog default")
		For("channel.impl").With("context", "ch{test}").Info("with bound attrs")
		if For("channel.impl").Enabled(context.Background(), LevelTrace) {
			// The fallback is Info-level, so trace being enabled means Enabled resolved
			// somewhere other than the fallback.
			t.Fatal("Enabled did not resolve through the fallback")
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run", "^"+t.Name()+"$", "-test.timeout", "30s")
	cmd.Env = append(os.Environ(), slogDefaultProbeVar+"=1")
	out, err := cmd.CombinedOutput()
	assert.NoError(t, err, "logging with a channel logger installed as the slog default must not recurse:\n%s", out)
}

// slogDefaultProbeVar marks the subprocess that TestForInstalledAsSlogDefault
// re-executes itself as.
const slogDefaultProbeVar = "CHANNEL_TEST_SLOG_DEFAULT_PROBE"
