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

package main

import (
	"github.com/michaelquigley/pfxlog"
	"github.com/openziti/channel/v4/cmd/channel/subcmd"
	"github.com/openziti/transport/v2"
	"github.com/openziti/transport/v2/tcp"
	"github.com/openziti/transport/v2/tls"
	"github.com/openziti/transport/v2/transwarp"
	"github.com/openziti/transport/v2/wss"
	"github.com/sirupsen/logrus"
)

func init() {
	transport.AddAddressParser(tcp.AddressParser{})
	transport.AddAddressParser(tls.AddressParser{})
	transport.AddAddressParser(transwarp.AddressParser{})
	transport.AddAddressParser(wss.AddressParser{})
	pfxlog.GlobalInit(logrus.InfoLevel, pfxlog.DefaultOptions().SetTrimPrefix("github.com/openziti/"))
}

func main() {
	if err := subcmd.Root.Execute(); err != nil {
		panic(err)
	}
}
