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

import "errors"

// RejectClass classifies why a listener refused a hello, so a dialer can tell a refusal apart from a
// network failure and from other refusals without matching on message text.
//
// A class describes the listener's state, not what the dialer should do about it. Retry policy belongs
// to the dialing side, which knows its own deadlines, alternatives and tolerance for delay; nothing in
// this library changes its behavior based on a received class.
//
// The set is closed and stable, so that a peer built against one version can rely on the values it
// knows. Application-specific detail belongs in the accompanying message.
type RejectClass int32

const (
	// RejectClassUnspecified is the class of a refusal that carries no classification, including one
	// from a listener too old to send one. It says only that the hello was refused.
	RejectClassUnspecified RejectClass = 0

	// RejectClassBusy indicates the listener could have served the request but has no capacity for it
	// now. The condition is a property of the listener rather than of the caller, so an identical
	// request may well succeed later or against another endpoint.
	RejectClassBusy RejectClass = 1

	// RejectClassNotPermitted indicates the listener will not serve this caller as it stands. Retrying
	// unchanged is unlikely to help; something about the caller's identity, configuration or
	// authorization has to change first.
	RejectClassNotPermitted RejectClass = 2
)

// String returns a short name for the class, for logs and metrics.
func (self RejectClass) String() string {
	switch self {
	case RejectClassBusy:
		return "busy"
	case RejectClassNotPermitted:
		return "not-permitted"
	default:
		return "unspecified"
	}
}

// RejectedError reports a refused hello, in both directions: an application returns one from an
// Admitter to classify its refusal, and a dialer receives one when a listener refuses its hello.
//
// A dialer receives it wrapped in a NonRetryableError, since a listener that declined a hello will
// decline the same hello again; recover it with errors.As rather than a type assertion. Whether to
// dial again later is the caller's decision, which is what the class is for.
type RejectedError struct {
	Class   RejectClass
	Message string
}

// Error returns the listener's message, or a class-derived description if the refusal carried no
// message.
func (self *RejectedError) Error() string {
	if self.Message != "" {
		return self.Message
	}
	if self.Class == RejectClassUnspecified {
		return "hello rejected"
	}
	return "hello rejected: " + self.Class.String()
}

// NewRejectedError creates a RejectedError with the given class and message.
func NewRejectedError(class RejectClass, message string) *RejectedError {
	return &RejectedError{Class: class, Message: message}
}

// GetRejectClass returns the class carried by err, or RejectClassUnspecified if err is nil or is not
// (and does not wrap) a RejectedError. An application that classifies nothing is therefore treated as
// classifying everything as unspecified.
func GetRejectClass(err error) RejectClass {
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		return rejected.Class
	}
	return RejectClassUnspecified
}

// RejectHello refuses an underlay whose hello has not been acknowledged, telling the dialer why before
// closing it. It is an alternative to closing the underlay directly, which leaves the dialer to infer a
// refusal from a connection that went away.
//
// The reason's text and, if it is a RejectedError, its class are sent as a negative hello result, which
// dialers have understood since long before this call existed; a dialer too old to read the class still
// gets the message. The underlay is always closed, whether or not the refusal could be sent.
//
// It must not be called after the hello has been acknowledged: the dialer is only listening for a hello
// result until it receives one.
func RejectHello(underlay Underlay, reason error) error {
	message := ""
	if reason != nil {
		message = reason.Error()
	}

	response := NewResult(false, message)
	if class := GetRejectClass(reason); class != RejectClassUnspecified {
		response.PutUint32Header(HelloRejectClassHeader, uint32(class))
	}

	// The hello exchange is the one place where a reply is not built from the request: the dialer
	// stamps its hello with HelloSequence and matches the reply against it, so the reply-for is known
	// without holding the request.
	response.sequence = HelloSequence
	replyFor := int32(HelloSequence)
	response.replyFor = &replyFor

	txErr := underlay.Tx(response)
	return errors.Join(txErr, underlay.Close())
}

// getRejectClass reads the class from a hello result message, defaulting to unspecified for a listener
// that sent none.
func getRejectClass(response *Message) RejectClass {
	if class, found := response.GetUint32Header(HelloRejectClassHeader); found {
		return RejectClass(class)
	}
	return RejectClassUnspecified
}
