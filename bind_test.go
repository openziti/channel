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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_BindHandlers_RunsAllHandlers(t *testing.T) {
	var calls []int

	h1 := BindHandlerF(func(Binding) error { calls = append(calls, 1); return nil })
	h2 := BindHandlerF(func(Binding) error { calls = append(calls, 2); return nil })
	h3 := BindHandlerF(func(Binding) error { calls = append(calls, 3); return nil })

	combined := BindHandlers(h1, h2, h3)
	err := combined.BindChannel(nil)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3}, calls)
}

func Test_BindHandlers_StopsOnFirstError(t *testing.T) {
	var calls []int
	testErr := errors.New("bind failed")

	h1 := BindHandlerF(func(Binding) error { calls = append(calls, 1); return nil })
	h2 := BindHandlerF(func(Binding) error { calls = append(calls, 2); return testErr })
	h3 := BindHandlerF(func(Binding) error { calls = append(calls, 3); return nil })

	combined := BindHandlers(h1, h2, h3)
	err := combined.BindChannel(nil)
	require.ErrorIs(t, err, testErr)
	require.Equal(t, []int{1, 2}, calls, "third handler should not have been called")
}

func Test_BindHandlers_SingleHandlerNotWrapped(t *testing.T) {
	called := false
	h := BindHandlerF(func(Binding) error { called = true; return nil })
	combined := BindHandlers(h)

	// Verify the returned handler is the original (not a wrapper)
	// by checking it's a BindHandlerF, not a nested closure
	_, ok := combined.(BindHandlerF)
	require.True(t, ok)

	err := combined.BindChannel(nil)
	require.NoError(t, err)
	require.True(t, called)
}

func Test_BindHandlers_SkipsNilHandlers(t *testing.T) {
	var calls []int

	h1 := BindHandlerF(func(Binding) error { calls = append(calls, 1); return nil })
	h2 := BindHandlerF(func(Binding) error { calls = append(calls, 2); return nil })

	combined := BindHandlers(h1, nil, h2)
	err := combined.BindChannel(nil)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, calls)
}
