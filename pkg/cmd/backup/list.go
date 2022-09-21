/*
Copyright © 2022 The OpenCli Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package backup

import (
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"

	"github.com/apecloud/kubeblocks/pkg/cmd/list"
)

func NewListCmd(f cmdutil.Factory, streams genericclioptions.IOStreams) *cobra.Command {
	cmd := list.Command{
		Factory:   f,
		Streams:   streams,
		Short:     "List all database backup job.",
		GroupKind: schema.GroupKind{Group: "dataprotection.infracreate.com", Kind: "BackupJob"},
	}
	return cmd.Build()
}
