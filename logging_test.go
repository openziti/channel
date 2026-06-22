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
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
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
