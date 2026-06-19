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
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEventLoggerResolution verifies the lifecycle-logger precedence:
// Options.Logger first, then the package-level LoggerFor, then a non-nil
// default.
func TestEventLoggerResolution(t *testing.T) {
	origLoggerFor := LoggerFor
	defer func() { LoggerFor = origLoggerFor }()

	optLogger := slog.New(slog.NewTextHandler(nil, nil))
	globalLogger := slog.New(slog.NewTextHandler(nil, nil))

	t.Run("Options.Logger wins over LoggerFor", func(t *testing.T) {
		LoggerFor = func(string) *slog.Logger { return globalLogger }
		ch := &channelImpl{logicalName: "link", options: &Options{Logger: optLogger}}
		assert.Same(t, optLogger, ch.eventLogger())
	})

	t.Run("LoggerFor used when Options.Logger nil", func(t *testing.T) {
		var gotName string
		LoggerFor = func(name string) *slog.Logger {
			gotName = name
			return globalLogger
		}
		ch := &channelImpl{logicalName: "ctrl", options: &Options{}}
		assert.Same(t, globalLogger, ch.eventLogger())
		assert.Equal(t, "ctrl", gotName, "LoggerFor should be keyed by the channel's logical name")
	})

	t.Run("default used when neither set", func(t *testing.T) {
		LoggerFor = nil
		ch := &channelImpl{logicalName: "agent", options: &Options{}}
		assert.NotNil(t, ch.eventLogger())
	})

	t.Run("nil options falls back to LoggerFor", func(t *testing.T) {
		LoggerFor = func(string) *slog.Logger { return globalLogger }
		ch := &channelImpl{logicalName: "agent"}
		assert.Same(t, globalLogger, ch.eventLogger())
	})
}
