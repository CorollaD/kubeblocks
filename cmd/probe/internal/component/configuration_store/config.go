package configuration_store

import (
	"bytes"
	"context"
	appsv1alpha1 "github.com/apecloud/kubeblocks/apis/apps/v1alpha1"
	"github.com/apecloud/kubeblocks/internal/cli/cluster"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"os"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/apecloud/kubeblocks/cmd/probe/util"
	"github.com/apecloud/kubeblocks/internal/cli/types"
)

type ConfigurationStore struct {
	ctx                  context.Context
	clusterName          string
	clusterCompName      string
	namespace            string
	cluster              *Cluster
	config               *rest.Config
	clientSet            *kubernetes.Clientset
	dynamicClient        *dynamic.DynamicClient
	leaderObservedRecord *LeaderRecord
	LeaderObservedTime   int64
}

func NewConfigurationStore() *ConfigurationStore {
	ctx := context.Background()
	config, err := clientcmd.BuildConfigFromFlags("", "/Users/buyanbujuan/.kube/config")
	if err != nil {
		panic(err)
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	return &ConfigurationStore{
		ctx:             ctx,
		clusterName:     os.Getenv(util.KbClusterName),
		clusterCompName: os.Getenv(util.KbClusterCompName),
		namespace:       os.Getenv(util.KbNamespace),
		config:          config,
		clientSet:       clientSet,
		dynamicClient:   dynamicClient,
		cluster:         &Cluster{},
	}
}

func (cs *ConfigurationStore) Init(sysID string, extra map[string]string) error {
	var getOpt metav1.GetOptions
	var updateOpt metav1.UpdateOptions
	var createOpt metav1.CreateOptions

	clusterObj, err := cs.dynamicClient.Resource(types.ClusterGVR()).Namespace(cs.namespace).Get(cs.ctx, cs.clusterName, getOpt)
	if err != nil {
		return err
	}

	leaderName := strings.Split(os.Getenv(util.KbPrimaryPodName), ".")[0]
	acquireTime := time.Now().Unix()
	renewTime := acquireTime
	ttl := os.Getenv(util.KbTtl)
	annotations := map[string]string{
		LeaderName:  leaderName,
		AcquireTime: strconv.FormatInt(acquireTime, 10),
		RenewTime:   strconv.FormatInt(renewTime, 10),
		TTL:         ttl,
	}
	clusterObj.SetAnnotations(annotations)
	if _, err = cs.dynamicClient.Resource(types.ClusterGVR()).Namespace(cs.namespace).Update(cs.ctx, clusterObj, updateOpt); err != nil {
		return err
	}

	maxLagOnFailover := os.Getenv(MaxLagOnFailover)
	if _, err = cs.clientSet.CoreV1().ConfigMaps(cs.namespace).Create(cs.ctx, &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cs.clusterCompName + ConfigSuffix,
			Namespace: cs.namespace,
			Annotations: map[string]string{
				SysID:            sysID,
				TTL:              ttl,
				MaxLagOnFailover: maxLagOnFailover,
			},
		},
	}, createOpt); err != nil {
		return err
	}

	if extra != nil {
		if _, err = cs.clientSet.CoreV1().ConfigMaps(cs.namespace).Create(cs.ctx, &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        cs.clusterCompName + ExtraSuffix,
				Namespace:   cs.namespace,
				Annotations: extra,
			},
		}, createOpt); err != nil {
			return err
		}
	}

	return nil
}

func (cs *ConfigurationStore) GetCluster() *Cluster {
	return cs.cluster
}

func (cs *ConfigurationStore) GetClusterName() string {
	return cs.clusterName
}

func (cs *ConfigurationStore) GetClusterFromKubernetes() error {
	podList, err := cs.clientSet.CoreV1().Pods(cs.namespace).List(cs.ctx, metav1.ListOptions{})
	if err != nil || podList == nil {
		return err
	}
	configMapList, err := cs.clientSet.CoreV1().ConfigMaps(cs.namespace).List(cs.ctx, metav1.ListOptions{})
	if err != nil || configMapList == nil {
		return err
	}
	clusterObj := &appsv1alpha1.Cluster{}
	if err = cluster.GetK8SClientObject(cs.dynamicClient, clusterObj, types.ClusterGVR(), cs.namespace, cs.clusterName); err != nil {
		return err
	}

	pods := make([]*v1.Pod, 0, len(podList.Items))
	for i, pod := range podList.Items {
		pods[i] = &pod
	}

	var config, failoverConfig *v1.ConfigMap
	for _, cf := range configMapList.Items {
		switch cf.Name {
		case cs.clusterCompName + ConfigSuffix:
			config = &cf
		case cs.clusterCompName + FailoverSuffix:
			failoverConfig = &cf
		}
	}

	cs.cluster = cs.loadClusterFromKubernetes(config, failoverConfig, pods, clusterObj, map[string]string{})

	return nil
}

func (cs *ConfigurationStore) loadClusterFromKubernetes(config, failoverConfig *v1.ConfigMap, pods []*v1.Pod, clusterObj *appsv1alpha1.Cluster, extra map[string]string) *Cluster {
	var (
		sysID         string
		clusterConfig *ClusterConfig
		leader        *Leader
		failover      *Failover
	)

	if config != nil {
		sysID = config.Annotations[SysID]
		clusterConfig = getClusterConfigFromConfigMap(config)
	}

	if clusterObj != nil {
		cs.leaderObservedRecord = newLeaderRecord(clusterObj.Annotations)
		cs.LeaderObservedTime = time.Now().Unix()
		leader = newLeader(clusterObj.ResourceVersion, newMember("-1", clusterObj.Annotations[LeaderName], map[string]string{}))
	}

	members := make([]*Member, 0, len(pods))
	for i, pod := range pods {
		members[i] = getMemberFromPod(pod)
	}

	if failover != nil {
		annotations := failoverConfig.Annotations
		scheduledAt, err := strconv.Atoi(annotations[ScheduledAt])
		if err == nil {
			failover = newFailover(failoverConfig.ResourceVersion, annotations[LeaderName], annotations[Candidate], int64(scheduledAt))
		}
	}

	return &Cluster{
		SysID:    sysID,
		Config:   clusterConfig,
		Leader:   leader,
		Members:  members,
		FailOver: failover,
		Extra:    extra,
	}
}

func (cs *ConfigurationStore) GetConfigMap(name string) (*v1.ConfigMap, error) {
	return cs.clientSet.CoreV1().ConfigMaps(cs.namespace).Get(cs.ctx, name, metav1.GetOptions{})
}

func (cs *ConfigurationStore) GetPod(name string) (*v1.Pod, error) {
	return cs.clientSet.CoreV1().Pods(cs.namespace).Get(cs.ctx, name, metav1.GetOptions{})
}

func (cs *ConfigurationStore) UpdateConfigMap(configMap *v1.ConfigMap) (*v1.ConfigMap, error) {
	return cs.clientSet.CoreV1().ConfigMaps(cs.namespace).Update(cs.ctx, configMap, metav1.UpdateOptions{})
}

func (cs *ConfigurationStore) ListPods() (*v1.PodList, error) {
	return cs.clientSet.CoreV1().Pods(cs.namespace).List(cs.ctx, metav1.ListOptions{})
}

func (cs *ConfigurationStore) GetClusterObj() (*unstructured.Unstructured, error) {
	return cs.dynamicClient.Resource(types.ClusterGVR()).Namespace(cs.namespace).Get(cs.ctx, cs.clusterName, metav1.GetOptions{})
}

func (cs *ConfigurationStore) UpdateClusterObj(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return cs.dynamicClient.Resource(types.ClusterGVR()).Namespace(cs.namespace).Update(cs.ctx, obj, metav1.UpdateOptions{})
}

func (cs *ConfigurationStore) ExecCmdWithPod(ctx context.Context, podName, cmd, container string) (map[string]string, error) {
	req := cs.clientSet.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(cs.namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Container: container,
			Command:   []string{"sh", "-c", cmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cs.config, "POST", req.URL())
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	if err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return nil, err
	}

	res := map[string]string{
		"stdout": stdout.String(),
		"stderr": stderr.String(),
	}

	return res, nil
}

type LeaderRecord struct {
	acquireTime int64
	leader      string
	renewTime   int64
	ttl         int64
}

func newLeaderRecord(data map[string]string) *LeaderRecord {
	ttl, err := strconv.Atoi(data[TTL])
	if err != nil {
		ttl = 0
	}

	acquireTime, err := strconv.Atoi(data[AcquireTime])
	if err != nil {
		acquireTime = 0
	}

	renewTime, err := strconv.Atoi(data[RenewTime])
	if err != nil {
		renewTime = 0
	}

	return &LeaderRecord{
		acquireTime: int64(acquireTime),
		leader:      data[LeaderName],
		renewTime:   int64(renewTime),
		ttl:         int64(ttl),
	}
}
