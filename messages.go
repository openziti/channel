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

// Built-in message content types used by the channel protocol.
const (
	ContentTypeHelloType           = 0
	ContentTypePingType            = 1
	ContentTypeResultType          = 2
	ContentTypeLatencyType         = 3
	ContentTypeLatencyResponseType = 4
	ContentTypeHeartbeat           = 5
	ContentTypeRaw                 = 6
)

// Hello is the initial handshake message exchanged when a connection is established.
type Hello struct {
	IdToken string
	Headers map[int32][]byte
}

// NewHello creates a hello Message with the given identity token and attributes.
func NewHello(idToken string, attributes map[int32][]byte) *Message {
	result := NewMessage(ContentTypeHelloType, []byte(idToken))
	for key, value := range attributes {
		result.Headers[key] = value
	}
	return result
}

// UnmarshalHello extracts a Hello from a received Message.
func UnmarshalHello(message *Message) *Hello {
	return &Hello{
		IdToken: string(message.Body),
		Headers: message.Headers,
	}
}

// NewResult creates a result Message indicating success or failure with a text message.
func NewResult(success bool, message string) *Message {
	msg := NewMessage(ContentTypeResultType, []byte(message))
	msg.PutBoolHeader(ResultSuccessHeader, success)
	return msg
}

// Result is a parsed result message containing a success flag and text.
type Result struct {
	Success bool
	Message string
}

// UnmarshalResult extracts a Result from a received Message.
func UnmarshalResult(message *Message) *Result {
	success, _ := message.GetBoolHeader(ResultSuccessHeader)
	resultMsg := string(message.Body)

	return &Result{
		Success: success,
		Message: resultMsg,
	}
}
