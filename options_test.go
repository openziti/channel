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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadOptions(t *testing.T) {
	req := require.New(t)

	data := map[interface{}]interface{}{
		"outQueueSize":           float64(10),
		"maxQueuedConnects":      20,
		"maxOutstandingConnects": 30,
		"connectTimeoutMs":       500,
		"writeTimeout":           "250ms",
	}

	options, err := LoadOptions(data)
	req.NoError(err)
	req.Equal(10, options.OutQueueSize)
	req.Equal(20, options.MaxQueuedConnects)
	req.Equal(30, options.MaxOutstandingConnects)
	req.Equal(500*time.Millisecond, options.ConnectTimeout)
	req.Equal(250*time.Millisecond, options.WriteTimeout)
}

func TestLoadOptionsDefaults(t *testing.T) {
	req := require.New(t)

	options, err := LoadOptions(map[interface{}]interface{}{})
	req.NoError(err)
	req.Equal(DefaultOutQueueSize, options.OutQueueSize)
	req.Equal(DefaultQueuedConnects, options.MaxQueuedConnects)
	req.Equal(DefaultOutstandingConnects, options.MaxOutstandingConnects)
	req.Equal(DefaultConnectTimeout, options.ConnectTimeout)
	req.Equal(time.Duration(0), options.WriteTimeout)
}

func TestLoadOnInstance(t *testing.T) {
	req := require.New(t)

	options := DefaultOptions()
	options.OutQueueSize = 16
	options.WriteTimeout = 500 * time.Millisecond

	data := map[interface{}]interface{}{
		"maxQueuedConnects": 100,
	}

	req.NoError(options.Load(data))

	// overridden by Load
	req.Equal(100, options.MaxQueuedConnects)

	// preserved from pre-configured instance
	req.Equal(16, options.OutQueueSize)
	req.Equal(500*time.Millisecond, options.WriteTimeout)

	// unchanged defaults
	req.Equal(DefaultOutstandingConnects, options.MaxOutstandingConnects)
	req.Equal(DefaultConnectTimeout, options.ConnectTimeout)
}

func TestLoadOptionsWriteTimeoutErrors(t *testing.T) {
	req := require.New(t)

	_, err := LoadOptions(map[interface{}]interface{}{
		"writeTimeout": 123,
	})
	req.Error(err)
	req.Contains(err.Error(), "invalid (non-string) value for writeTimeout")

	_, err = LoadOptions(map[interface{}]interface{}{
		"writeTimeout": "not-a-duration",
	})
	req.Error(err)
	req.Contains(err.Error(), "invalid value for writeTimeout")
}
