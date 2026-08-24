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
	"fmt"
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

// Test_ReplyForMalformed covers a ReplyFor header that is present but not 4 bytes wide.
// The getters must return the not-a-reply default rather than dereferencing a nil cache.
func Test_ReplyForMalformed(t *testing.T) {
	// backing array so a zero-length header value is a non-nil empty slice, as it is
	// when sliced out of wire data
	backing := make([]byte, 16)

	for _, length := range []int{0, 1, 3, 5, 8} {
		t.Run(fmt.Sprintf("len-%d", length), func(t *testing.T) {
			m := NewMessage(1, nil)
			m.Headers[ReplyForHeader] = backing[:length]

			require.NotPanics(t, func() {
				assert.False(t, m.IsReply())
				assert.Equal(t, int32(-1), m.ReplyFor())
				assert.False(t, m.IsReplyingTo(1))
			})
		})
	}
}

func Test_ReplyForWellFormed(t *testing.T) {
	m := NewMessage(1, nil)
	m.PutUint32Header(ReplyForHeader, 7)

	assert.True(t, m.IsReply())
	assert.Equal(t, int32(7), m.ReplyFor())
	assert.True(t, m.IsReplyingTo(7))
	assert.False(t, m.IsReplyingTo(8))
}

func Test_ReplyForAbsent(t *testing.T) {
	m := NewMessage(1, nil)

	assert.False(t, m.IsReply())
	assert.Equal(t, int32(-1), m.ReplyFor())
}

// Test_unmarshalHeadersRejectsBadReplyFor asserts a malformed ReplyFor is rejected at
// unmarshal, so the frame is dropped rather than silently treated as a non-reply.
func Test_unmarshalHeadersRejectsBadReplyFor(t *testing.T) {
	buildHeader := func(key int32, val []byte) []byte {
		buf := make([]byte, 8+len(val))
		binary.LittleEndian.PutUint32(buf[0:4], uint32(key))
		binary.LittleEndian.PutUint32(buf[4:8], uint32(len(val)))
		copy(buf[8:], val)
		return buf
	}

	for _, length := range []int{0, 1, 3, 5, 8} {
		t.Run(fmt.Sprintf("len-%d", length), func(t *testing.T) {
			_, err := unmarshalHeaders(buildHeader(ReplyForHeader, make([]byte, length)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid replyFor header length")
		})
	}

	t.Run("valid", func(t *testing.T) {
		headers, err := unmarshalHeaders(buildHeader(ReplyForHeader, []byte{1, 0, 0, 0}))
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 0, 0, 0}, headers[ReplyForHeader])
	})

	t.Run("other header any length", func(t *testing.T) {
		headers, err := unmarshalHeaders(buildHeader(TypeHeader, []byte{1, 2, 3}))
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3}, headers[TypeHeader])
	})
}

// buildHeaderBlock lays out one header in the on-wire format: key, declared length, value.
// declaredLen is passed separately from the value so a test can declare a length the value
// does not have.
func buildHeaderBlock(key int32, declaredLen uint32, val []byte) []byte {
	buf := make([]byte, 8+len(val))
	binary.LittleEndian.PutUint32(buf[0:4], uint32(key))
	binary.LittleEndian.PutUint32(buf[4:8], declaredLen)
	copy(buf[8:], val)
	return buf
}

// Test_unmarshalHeadersLengthOverflow covers a header whose declared length does not fit in
// an int on a 32-bit platform. The bounds check must reject it rather than converting first.
//
// NOTE: on a 64-bit platform int(length) is exact, so this passes with or without the fix.
// It only fails on the unfixed code under GOARCH=386.
func Test_unmarshalHeadersLengthOverflow(t *testing.T) {
	for _, declared := range []uint32{0x80000000, 0xFFFFFFF0, 0xFFFFFFFF} {
		t.Run(fmt.Sprintf("declared-%#x", declared), func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := unmarshalHeaders(buildHeaderBlock(7, declared, nil))
				require.Error(t, err)
				assert.Contains(t, err.Error(), "short header data")
			})
		})
	}
}

// Test_unmarshalV2LengthWrap covers declared lengths whose uint32 sum wraps. The wrapped
// total agrees with every check that follows, so the header slice is what panics.
func Test_unmarshalV2LengthWrap(t *testing.T) {
	messageSection := make([]byte, dataSectionV2)
	copy(messageSection[0:magicLength], magicV2)

	for _, tc := range []struct {
		name          string
		headersLength uint32
		bodyLength    uint32
	}{
		{name: "sum wraps to zero", headersLength: 0xFFFFFFFF, bodyLength: 1},
		{name: "sum wraps to small", headersLength: 0xFFFFFFF0, bodyLength: 0x20},
		{name: "sum exceeds MaxInt32 without wrapping", headersLength: 0x7FFFFFFF, bodyLength: 0x7FFFFFFF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := unmarshalV2(bytes.NewReader(nil), messageSection, tc.headersLength, tc.bodyLength)
				require.Error(t, err)
			})
		})
	}
}
