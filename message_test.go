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
	"github.com/stretchr/testify/assert"
	"testing"
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
