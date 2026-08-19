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
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_getRetryVersionFor(t *testing.T) {
	twoAndOne := []uint32{2, 1}

	tests := []struct {
		name          string
		err           error
		localVersions []uint32
		want          uint32
		want1         bool
	}{
		struct {
			name          string
			err           error
			localVersions []uint32
			want          uint32
			want1         bool
		}{name: "non version error", err: errors.New("foo"), localVersions: twoAndOne, want: 1, want1: false},
		{name: "empty non version error", err: UnsupportedVersionError{}, localVersions: twoAndOne, want: 1, want1: false},
		{name: "v1", err: UnsupportedVersionError{supportedVersions: []uint32{1}}, localVersions: twoAndOne, want: 1, want1: true},
		{name: "v1, v2", err: UnsupportedVersionError{supportedVersions: []uint32{1, 2}}, localVersions: twoAndOne, want: 2, want1: true},
		{name: "v2, v1", err: UnsupportedVersionError{supportedVersions: []uint32{2, 1}}, localVersions: twoAndOne, want: 2, want1: true},
		{name: "v2", err: UnsupportedVersionError{supportedVersions: []uint32{2}}, localVersions: twoAndOne, want: 2, want1: true},
		{name: "v3", err: UnsupportedVersionError{supportedVersions: []uint32{3}}, localVersions: twoAndOne, want: 1, want1: false},
		{name: "v1, v2, v3", err: UnsupportedVersionError{supportedVersions: []uint32{1, 2, 3}}, localVersions: twoAndOne, want: 2, want1: true},
		{name: "v3, v2, v1", err: UnsupportedVersionError{supportedVersions: []uint32{3, 2, 1}}, localVersions: twoAndOne, want: 2, want1: true},
		{name: "v3, v1", err: UnsupportedVersionError{supportedVersions: []uint32{1, 3}}, localVersions: twoAndOne, want: 1, want1: true},
		{name: "v1, v3", err: UnsupportedVersionError{supportedVersions: []uint32{3, 1}}, localVersions: twoAndOne, want: 1, want1: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := getRetryVersionFor(tt.err, 1, tt.localVersions...)
			if got != tt.want {
				t.Errorf("getRetryVersionFor() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("getRetryVersionFor() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

// Test_readUnknownVersionResponse covers parsing a version negotiation response, whose bytes may
// arrive alongside the magic, after it, or split between the two, since the magic is classified as
// soon as it is read rather than after a whole frame's worth of bytes.
func Test_readUnknownVersionResponse(t *testing.T) {
	// encode builds the body of a version response: a count followed by that many versions.
	encode := func(versions ...uint32) []byte {
		body := binary.LittleEndian.AppendUint32(nil, uint32(len(versions)))
		for _, version := range versions {
			body = binary.LittleEndian.AppendUint32(body, version)
		}
		return body
	}

	supportedVersions := func(t *testing.T, err error) []uint32 {
		t.Helper()
		var versionErr UnsupportedVersionError
		require.ErrorAs(t, err, &versionErr)
		return versionErr.supportedVersions
	}

	t.Run("wholly buffered", func(t *testing.T) {
		err := readUnknownVersionResponse(encode(1, 2), bytes.NewReader(nil))
		require.Equal(t, []uint32{1, 2}, supportedVersions(t, err))
	})

	t.Run("wholly unread", func(t *testing.T) {
		err := readUnknownVersionResponse(nil, bytes.NewReader(encode(1, 2)))
		require.Equal(t, []uint32{1, 2}, supportedVersions(t, err))
	})

	t.Run("split across the buffer and the wire", func(t *testing.T) {
		body := encode(1, 2)
		for split := range len(body) {
			err := readUnknownVersionResponse(body[:split], bytes.NewReader(body[split:]))
			require.Equal(t, []uint32{1, 2}, supportedVersions(t, err), "split after %v bytes", split)
		}
	})

	t.Run("trailing bytes are not versions", func(t *testing.T) {
		body := append(encode(2), 0xde, 0xad, 0xbe, 0xef)
		err := readUnknownVersionResponse(body, bytes.NewReader(nil))
		require.Equal(t, []uint32{2}, supportedVersions(t, err), "only the counted versions should be parsed")
	})

	t.Run("truncated response", func(t *testing.T) {
		body := encode(1, 2)
		err := readUnknownVersionResponse(body[:len(body)-1], bytes.NewReader(nil))
		require.Error(t, err)
		require.NotErrorAs(t, err, &UnsupportedVersionError{}, "a truncated response reports no versions")
	})

	t.Run("implausible version count", func(t *testing.T) {
		// The versions are supplied too, so the count is refused on its own merits rather than
		// because the response ran out of bytes.
		body := binary.LittleEndian.AppendUint32(nil, maxSupportedVersionCount+1)
		body = append(body, make([]byte, (maxSupportedVersionCount+1)*versionLen)...)

		err := readUnknownVersionResponse(body, bytes.NewReader(nil))
		require.Error(t, err)
		require.NotErrorAs(t, err, &UnsupportedVersionError{}, "an implausible count must be refused, not allocated")
	})
}

// Test_ReadV2_ClassifiesVersionResponseShorterThanAFrame verifies a version negotiation response is
// recognized even though it is shorter than a message frame, which is what a listener actually sends
// before closing the connection.
func Test_ReadV2_ClassifiesVersionResponseShorterThanAFrame(t *testing.T) {
	req := require.New(t)

	response := new(bytes.Buffer)
	WriteUnknownVersionResponse(response)
	req.Less(response.Len(), dataSectionV2, "the response must be shorter than a frame for this to be a test")

	_, err := ReadV2(bytes.NewReader(response.Bytes()))

	var versionErr UnsupportedVersionError
	req.ErrorAs(err, &versionErr)
	req.Equal([]uint32{1, 2}, versionErr.supportedVersions)
}

func Test_StrSliceEncodeDecode(t *testing.T) {
	req := assert.New(t)

	test := func(s []string) {
		encoded := EncodeStringSlice(s)
		decoded, err := DecodeStringSlice(encoded)
		req.NoError(err)
		req.Equal(s, decoded)
	}
	test(nil)
	test([]string{""})
	test([]string{"hello"})
	test([]string{"hello", ""})
	test([]string{"", "hello"})
	test([]string{"hello", "how are you!", "i hope things are ok"})

	encoded := EncodeStringSlice([]string{})
	decoded, err := DecodeStringSlice(encoded)
	req.NoError(err)
	req.Equal(([]string)(nil), decoded)
}

func Test_U32ToBytesMapEncodeDecode(t *testing.T) {
	req := assert.New(t)

	test := func(m map[uint32][]byte) {
		encoded := EncodeU32ToBytesMap(m)
		decoded, err := DecodeU32ToBytesMap(encoded)
		req.NoError(err)
		req.Equal(m, decoded)
	}
	test(nil)
	test(map[uint32][]byte{
		1: nil,
	})
	test(map[uint32][]byte{
		1: nil,
		2: []byte("hello"),
	})
	test(map[uint32][]byte{
		1: []byte("hello"),
		2: nil,
	})

	test(map[uint32][]byte{
		100: nil,
		200: []byte("hello"),
		300: []byte("good bye there"),
	})

	test(map[uint32][]byte{
		100: []byte("some more entries here for good measure"),
		200: []byte("hello"),
		300: []byte("good bye there"),
	})

	encoded := EncodeU32ToBytesMap(map[uint32][]byte{})
	decoded, err := DecodeU32ToBytesMap(encoded)
	req.NoError(err)
	req.Equal((map[uint32][]byte)(nil), decoded)
}

func Test_StringToStringMapEncodeDecode(t *testing.T) {
	req := assert.New(t)

	test := func(m map[string]string) {
		encoded := EncodeStringToStringMap(m)
		decoded, err := DecodeStringToStringMap(encoded)
		req.NoError(err)
		req.Equal(m, decoded)
	}
	test(nil)
	test(map[string]string{
		"one": "",
	})
	test(map[string]string{
		"one": "",
		"two": "hello",
	})
	test(map[string]string{
		"one": "hello",
		"two": "",
	})

	test(map[string]string{
		"one hundred": "",
		"other":       "hello",
		"different":   "good bye there",
	})

	test(map[string]string{
		"foo":  "some more entries here for good measure",
		"bart": "hello",
		"quux": "good bye there",
	})

	encoded := EncodeStringToStringMap(map[string]string{})
	decoded, err := DecodeStringToStringMap(encoded)
	req.NoError(err)
	req.Equal((map[string]string)(nil), decoded)
}

func Test_ReplyToReflectsOnlyTheReflectedHeader(t *testing.T) {
	req := NewMessage(1, nil)
	req.sequence = 42
	req.Headers[ReflectedHeader] = []byte("correlation-value")
	req.Headers[ConnectionIdHeader] = []byte("not reflected")

	// 129-255 were reflected before reflection narrowed to a single header
	req.Headers[129] = []byte("formerly reflected")
	req.Headers[200] = []byte("formerly reflected")
	req.Headers[255] = []byte("formerly reflected")

	reply := NewMessage(2, nil)
	reply.ReplyTo(req)

	assert.True(t, reply.IsReplyingTo(42))
	assert.Equal(t, []byte("correlation-value"), reply.Headers[ReflectedHeader])

	for _, key := range []int32{ConnectionIdHeader, 129, 200, 255} {
		_, found := reply.Headers[key]
		assert.False(t, found, "header %v should not be reflected", key)
	}
}

func Test_ReplyToWithoutReflectedHeaderAddsNothing(t *testing.T) {
	req := NewMessage(1, nil)
	req.sequence = 7

	reply := NewMessage(2, nil)
	reply.ReplyTo(req)

	assert.True(t, reply.IsReplyingTo(7))
	_, found := reply.Headers[ReflectedHeader]
	assert.False(t, found, "no reflected header should be added when the request had none")
}
