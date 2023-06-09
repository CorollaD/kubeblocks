/*
Copyright (C) 2022-2023 ApeCloud Co., Ltd

This file is part of KubeBlocks project

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package dataprotection

import (
	"fmt"

	"github.com/spf13/viper"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dataprotectionv1alpha1 "github.com/apecloud/kubeblocks/apis/dataprotection/v1alpha1"
	"github.com/apecloud/kubeblocks/internal/constant"
	intctrlutil "github.com/apecloud/kubeblocks/internal/controllerutil"
)

// byBackupStartTime sorts a list of jobs by start timestamp, using their names as a tie breaker.
type byBackupStartTime []dataprotectionv1alpha1.Backup

// Len returns the length of byBackupStartTime, for the sort.Sort
func (o byBackupStartTime) Len() int { return len(o) }

// Swap the items, for the sort.Sort
func (o byBackupStartTime) Swap(i, j int) { o[i], o[j] = o[j], o[i] }

// Less defines how to compare items, for the sort.Sort
func (o byBackupStartTime) Less(i, j int) bool {
	if o[i].Status.StartTimestamp == nil && o[j].Status.StartTimestamp != nil {
		return false
	}
	if o[i].Status.StartTimestamp != nil && o[j].Status.StartTimestamp == nil {
		return true
	}
	if o[i].Status.StartTimestamp.Equal(o[j].Status.StartTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].Status.StartTimestamp.Before(o[j].Status.StartTimestamp)
}

// getBackupToolByName gets the backupTool by name.
func getBackupToolByName(reqCtx intctrlutil.RequestCtx, cli client.Client, backupName string) (*dataprotectionv1alpha1.BackupTool, error) {
	backupTool := &dataprotectionv1alpha1.BackupTool{}
	backupToolNameSpaceName := types.NamespacedName{
		Name: backupName,
	}
	if err := cli.Get(reqCtx.Ctx, backupToolNameSpaceName, backupTool); err != nil {
		reqCtx.Log.Error(err, "Unable to get backupTool for backup.", "BackupTool", backupToolNameSpaceName)
		return nil, err
	}
	return backupTool, nil
}

// getCreatedCRNameByBackupPolicy gets the CR name which is created by BackupPolicy, such as CronJob/logfile Backup.
func getCreatedCRNameByBackupPolicy(backupPolicyName, backupPolicyNamespace string, backupType dataprotectionv1alpha1.BackupType) string {
	name := fmt.Sprintf("%s-%s", backupPolicyName, backupPolicyNamespace)
	if len(name) > 30 {
		name = name[:30]
	}
	return fmt.Sprintf("%s-%s", name, string(backupType))
}

// getBackupBatchV1Job gets the v1 job which is created by backup.
func getBackupBatchV1Job(reqCtx intctrlutil.RequestCtx, cli client.Client, backup *dataprotectionv1alpha1.Backup) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	jobNameSpaceName := types.NamespacedName{
		Namespace: reqCtx.Req.Namespace,
		Name:      backup.Name,
	}
	if err := cli.Get(reqCtx.Ctx, jobNameSpaceName, job); err != nil {
		// not found backup, do nothing
		reqCtx.Log.Info(err.Error())
		return nil, err
	}
	return job, nil
}

func buildBackupLabels(backup *dataprotectionv1alpha1.Backup) map[string]string {
	labels := backup.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	labels[dataProtectionLabelBackupNameKey] = backup.Name
	return labels
}

func addTolerations(podSpec *corev1.PodSpec) (err error) {
	if cmTolerations := viper.GetString(constant.CfgKeyCtrlrMgrTolerations); cmTolerations != "" {
		if err = json.Unmarshal([]byte(cmTolerations), &podSpec.Tolerations); err != nil {
			return err
		}
	}
	if cmAffinity := viper.GetString(constant.CfgKeyCtrlrMgrAffinity); cmAffinity != "" {
		if err = json.Unmarshal([]byte(cmAffinity), &podSpec.Affinity); err != nil {
			return err
		}
	}
	if cmNodeSelector := viper.GetString(constant.CfgKeyCtrlrMgrNodeSelector); cmNodeSelector != "" {
		if err = json.Unmarshal([]byte(cmNodeSelector), &podSpec.NodeSelector); err != nil {
			return err
		}
	}
	return nil
}
