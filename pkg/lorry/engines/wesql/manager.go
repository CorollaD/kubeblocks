/*
Copyright (C) 2022-2024 ApeCloud Co., Ltd

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

package wesql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/apecloud/kubeblocks/pkg/lorry/dcs"
	"github.com/apecloud/kubeblocks/pkg/lorry/engines"
	"github.com/apecloud/kubeblocks/pkg/lorry/engines/mysql"
)

const (
	Role          = "ROLE"
	CurrentLeader = "CURRENT_LEADER"
	Leader        = "Leader"
)

type Manager struct {
	mysql.Manager
}

var _ engines.DBManager = &Manager{}

func NewManager(properties engines.Properties) (engines.DBManager, error) {
	logger := ctrl.Log.WithName("WeSQL")
	_, err := NewConfig(properties)
	if err != nil {
		return nil, err
	}

	mysqlMgr, err := mysql.NewManager(properties)
	if err != nil {
		return nil, err
	}

	mgr := &Manager{
		Manager: *mysqlMgr.(*mysql.Manager),
	}

	mgr.SetLogger(logger)
	return mgr, nil
}

func (mgr *Manager) InitializeCluster(context.Context, *dcs.Cluster) error {
	return nil
}

func (mgr *Manager) IsLeader(ctx context.Context, cluster *dcs.Cluster) (bool, error) {
	role, err := mgr.GetReplicaRole(ctx, cluster)

	if err != nil {
		return false, err
	}

	if strings.EqualFold(role, Leader) {
		return true, nil
	}

	return false, nil
}

func (mgr *Manager) IsLeaderMember(_ context.Context, cluster *dcs.Cluster, member *dcs.Member) (bool, error) {
	if member == nil {
		return false, nil
	}

	leaderMember := mgr.GetLeaderMember(cluster)
	if leaderMember == nil {
		return false, nil
	}

	if leaderMember.Name != member.Name {
		return false, nil
	}

	return true, nil
}

func (mgr *Manager) InitiateCluster(_ *dcs.Cluster) error {
	return nil
}

func (mgr *Manager) GetMemberAddrs(ctx context.Context, cluster *dcs.Cluster) []string {
	addrs := make([]string, 0, 3)
	clusterInfo := mgr.GetClusterInfo(ctx, cluster)
	clusterInfo = strings.Split(clusterInfo, "@")[0]
	for _, addr := range strings.Split(clusterInfo, ";") {
		if !strings.Contains(addr, ":") {
			continue
		}
		addrs = append(addrs, strings.Split(addr, "#")[0])
	}

	return addrs
}

func (mgr *Manager) GetAddrWithMemberName(ctx context.Context, cluster *dcs.Cluster, memberName string) string {
	addrs := mgr.GetMemberAddrs(ctx, cluster)
	for _, addr := range addrs {
		if strings.HasPrefix(addr, memberName) {
			return addr
		}
	}
	return ""
}

func (mgr *Manager) IsCurrentMemberInCluster(ctx context.Context, cluster *dcs.Cluster) bool {
	clusterInfo := mgr.GetClusterInfo(ctx, cluster)
	return strings.Contains(clusterInfo, mgr.CurrentMemberName)
}

func (mgr *Manager) IsMemberLagging(context.Context, *dcs.Cluster, *dcs.Member) (bool, int64) {
	return false, 0
}

func (mgr *Manager) Recover(context.Context, *dcs.Cluster) error {
	return nil
}

func (mgr *Manager) JoinCurrentMemberToCluster(context.Context, *dcs.Cluster) error {
	return nil
}

func (mgr *Manager) LeaveMemberFromCluster(ctx context.Context, cluster *dcs.Cluster, memberName string) error {
	db, err := mgr.GetLeaderConn(cluster)
	if err != nil {
		mgr.Logger.Error(err, "Get leader conn failed")
		return err
	}
	addr := mgr.GetAddrWithMemberName(ctx, cluster, memberName)
	if addr == "" {
		mgr.Logger.Info(fmt.Sprintf("member %s already deleted", memberName))
		return nil
	}

	sql := fmt.Sprintf("call dbms_consensus.downgrade_follower('%s');"+
		"call dbms_consensus.drop_learner('%s');", addr, addr)
	_, err = db.ExecContext(ctx, sql)
	if err != nil {
		mgr.Logger.Error(err, "delete member from db cluster failed")
		return errors.Wrapf(err, "error executing %s", sql)
	}
	return nil
}

func (mgr *Manager) IsClusterHealthy(_ context.Context, cluster *dcs.Cluster) bool {
	db, err := mgr.GetLeaderConn(cluster)
	if err != nil {
		mgr.Logger.Error(err, "Get leader conn failed")
		return false
	}

	var leaderRecord mysql.RowMap
	sql := "select * from information_schema.wesql_cluster_global;"
	err = mysql.QueryRowsMap(db, sql, func(rMap mysql.RowMap) error {
		if rMap.GetString(Role) == Leader {
			leaderRecord = rMap
		}
		return nil
	})
	if err != nil {
		mgr.Logger.Error(err, fmt.Sprintf("error executing %s", sql))
		return false
	}

	if len(leaderRecord) > 0 {
		return true
	}
	return false
}

// IsClusterInitialized is a method to check if cluster is initialized or not
func (mgr *Manager) IsClusterInitialized(ctx context.Context, _ *dcs.Cluster) (bool, error) {
	clusterInfo := mgr.GetClusterInfo(ctx, nil)
	if clusterInfo != "" {
		return true, nil
	}

	return false, nil
}

func (mgr *Manager) GetClusterInfo(ctx context.Context, cluster *dcs.Cluster) string {
	var db *sql.DB
	var err error
	if cluster != nil {
		db, err = mgr.GetLeaderConn(cluster)
		if err != nil {
			mgr.Logger.Error(err, "Get leader conn failed")
			return ""
		}
	} else {
		db = mgr.DB

	}
	var clusterID, clusterInfo string
	err = db.QueryRowContext(ctx, "select cluster_id, cluster_info from mysql.consensus_info").
		Scan(&clusterID, &clusterInfo)
	if err != nil {
		mgr.Logger.Error(err, "Cluster info query failed")
	}
	return clusterInfo
}

func (mgr *Manager) Promote(ctx context.Context, cluster *dcs.Cluster) error {
	isLeader, _ := mgr.IsLeader(ctx, nil)
	if isLeader {
		return nil
	}

	db, err := mgr.GetLeaderConn(cluster)
	if err != nil {
		return errors.Wrap(err, "Get leader conn failed")
	}

	addr := mgr.GetAddrWithMemberName(ctx, cluster, mgr.CurrentMemberName)
	if addr == "" {
		return errors.New("get current member's addr failed")
	}
	resp, err := db.Exec(fmt.Sprintf("call dbms_consensus.change_leader('%s');", addr))
	if err != nil {
		return err
	}

	mgr.Logger.Info("promote success", "resp", resp)
	return nil
}

func (mgr *Manager) IsPromoted(ctx context.Context) bool {
	isLeader, _ := mgr.IsLeader(ctx, nil)
	return isLeader
}

func (mgr *Manager) Demote(context.Context) error {
	return nil
}

func (mgr *Manager) Follow(_ context.Context, cluster *dcs.Cluster) error {
	mgr.Logger.Info("current member still follow the leader", "leader name", cluster.Leader.Name)
	return nil
}

func (mgr *Manager) GetHealthiestMember(*dcs.Cluster, string) *dcs.Member {
	return nil
}

func (mgr *Manager) HasOtherHealthyLeader(_ context.Context, cluster *dcs.Cluster) *dcs.Member {
	clusterLocalInfo, err := mgr.GetClusterLocalInfo()
	if err != nil || clusterLocalInfo == nil {
		mgr.Logger.Error(err, "Get cluster local info failed")
		return nil
	}

	if clusterLocalInfo.GetString(Role) == Leader {
		// I am the leader, just return nil
		return nil
	}

	leaderAddr := clusterLocalInfo.GetString(CurrentLeader)
	if leaderAddr == "" {
		return nil
	}
	leaderParts := strings.Split(leaderAddr, ".")
	if len(leaderParts) > 0 {
		return cluster.GetMemberWithName(leaderParts[0])
	}

	return nil
}

// HasOtherHealthyMembers checks if there are any healthy members, excluding the leader
func (mgr *Manager) HasOtherHealthyMembers(ctx context.Context, cluster *dcs.Cluster, leader string) []*dcs.Member {
	members := make([]*dcs.Member, 0)
	for _, member := range cluster.Members {
		if member.Name == leader {
			continue
		}
		if !mgr.IsMemberHealthy(ctx, cluster, &member) {
			continue
		}
		members = append(members, &member)
	}

	return members
}
