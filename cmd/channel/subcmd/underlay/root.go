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

package underlay

import (
	"github.com/openziti/channel/v4/cmd/channel/subcmd"
	"github.com/spf13/cobra"
)

func init() {
	subcmd.Root.AddCommand(underlay)
}

var underlay = &cobra.Command{
	Use:   "underlay",
	Short: "Channel2 underlay tools",
}
