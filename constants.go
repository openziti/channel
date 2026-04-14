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

import "time"

// Connection and queue size defaults, minimums, and maximums.
const (
	DefaultOutstandingConnects = 16
	DefaultQueuedConnects      = 1
	DefaultConnectTimeout      = 5 * time.Second

	MinQueuedConnects      = 1
	MinOutstandingConnects = 1
	MinConnectTimeout      = 30 * time.Millisecond

	MaxQueuedConnects      = 5000
	MaxOutstandingConnects = 1000
	MaxConnectTimeout      = 60000 * time.Millisecond

	// DefaultOutQueueSize is the default capacity of the outgoing message queue.
	DefaultOutQueueSize = 4
)
