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

func Test_GetUnderlayType_ReturnsTypeHeader(t *testing.T) {
	u := &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("priority")}}
	require.Equal(t, "priority", GetUnderlayType(u))
}

func Test_GetUnderlayType_DefaultWhenMissing(t *testing.T) {
	u := &testUnderlay{headers: map[int32][]byte{}}
	require.Equal(t, DefaultUnderlayType, GetUnderlayType(u))
}

func Test_GetUnderlayType_DefaultWhenNilHeaders(t *testing.T) {
	u := &testUnderlay{}
	require.Equal(t, DefaultUnderlayType, GetUnderlayType(u))
}

func Test_GetValidatedUnderlayType_NoValidList(t *testing.T) {
	ch := &channelImpl{}
	u := &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("anything")}}
	require.Equal(t, "anything", ch.getValidatedUnderlayType(u))
}

func Test_GetValidatedUnderlayType_KnownType(t *testing.T) {
	ch := &channelImpl{validUnderlayTypes: []string{"tcp", "ws"}}
	u := &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("ws")}}
	require.Equal(t, "ws", ch.getValidatedUnderlayType(u))
}

func Test_GetValidatedUnderlayType_UnknownTypeMapsToDefault(t *testing.T) {
	ch := &channelImpl{validUnderlayTypes: []string{"tcp", "ws"}}
	u := &testUnderlay{headers: map[int32][]byte{TypeHeader: []byte("quic")}}
	require.Equal(t, DefaultUnderlayType, ch.getValidatedUnderlayType(u))
}

func Test_GetValidatedUnderlayType_MissingHeaderMapsToDefault(t *testing.T) {
	ch := &channelImpl{validUnderlayTypes: []string{"tcp", "ws"}}
	u := &testUnderlay{headers: map[int32][]byte{}}
	// GetUnderlayType returns DefaultUnderlayType, which is not in the valid list
	// unless it's explicitly included
	require.Equal(t, DefaultUnderlayType, ch.getValidatedUnderlayType(u))
}
