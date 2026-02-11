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

	"github.com/stretchr/testify/require"
)

func Test_SimpleMessageSourceProvider_ReturnsRegisteredSource(t *testing.T) {
	defaultCalled := false
	registeredCalled := false

	defaultSource := func(*CloseNotifier) (Sendable, error) {
		defaultCalled = true
		return nil, nil
	}

	registeredSource := func(*CloseNotifier) (Sendable, error) {
		registeredCalled = true
		return nil, nil
	}

	p := NewSimpleMessageSourceProvider(defaultSource)
	p.AddSource("priority", registeredSource)

	src := p.GetMessageSource("priority")
	_, _ = src(nil)

	require.True(t, registeredCalled)
	require.False(t, defaultCalled)
}

func Test_SimpleMessageSourceProvider_FallsBackToDefault(t *testing.T) {
	defaultCalled := false

	defaultSource := func(*CloseNotifier) (Sendable, error) {
		defaultCalled = true
		return nil, nil
	}

	p := NewSimpleMessageSourceProvider(defaultSource)
	p.AddSource("priority", func(*CloseNotifier) (Sendable, error) {
		return nil, nil
	})

	src := p.GetMessageSource("unknown-type")
	_, _ = src(nil)

	require.True(t, defaultCalled)
}

func Test_SimpleMessageSourceProvider_DefaultForEmptyType(t *testing.T) {
	defaultCalled := false

	defaultSource := func(*CloseNotifier) (Sendable, error) {
		defaultCalled = true
		return nil, nil
	}

	p := NewSimpleMessageSourceProvider(defaultSource)

	src := p.GetMessageSource("")
	_, _ = src(nil)

	require.True(t, defaultCalled)
}
