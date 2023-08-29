/*
Copyright 2019 The Kubernetes Authors.

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

package serviceaccount

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/syncer/constants"
	"sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/syncer/metrics"
	"sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/syncer/patrol/differ"
	"sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/syncer/util"
)

func (c *controller) StartPatrol(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	if !cache.WaitForCacheSync(stopCh, c.saSynced) {
		return fmt.Errorf("failed to wait for caches to sync before starting SA checker")
	}
	c.Patroller.Start(stopCh)
	return nil
}

// PatrollerDo checks to see if serviceaccounts in super control plane informer cache and tenant control plane
// keep consistency.
func (c *controller) PatrollerDo() {
	clusterNames := c.MultiClusterController.GetClusterNames()
	if len(clusterNames) == 0 {
		klog.V(5).Infof("super cluster has no tenant control planes, giving up periodic checker: %s", "serviceaccount")
		return
	}

	pList, err := c.saLister.List(util.GetSuperClusterListerLabelsSelector())
	if err != nil {
		klog.Errorf("error listing service accounts from super control plane informer cache: %v", err)
		return
	}
	pSet := differ.NewDiffSet()
	for _, p := range pList {
		pSet.Insert(differ.ClusterObject{Object: p, Key: differ.DefaultClusterObjectKey(p, "")})
	}

	knownClusterSet := sets.NewString(clusterNames...)
	vSet := differ.NewDiffSet()
	for _, cluster := range clusterNames {
		vList := &corev1.ServiceAccountList{}
		if err := c.MultiClusterController.List(cluster, vList); err != nil {
			klog.Errorf("error listing service accounts from cluster %s informer cache: %v", cluster, err)
			knownClusterSet.Delete(cluster)
			continue
		}

		for i := range vList.Items {
			vSet.Insert(differ.ClusterObject{
				Object:       &vList.Items[i],
				OwnerCluster: cluster,
				Key:          differ.DefaultClusterObjectKey(&vList.Items[i], cluster),
			})
		}
	}

	d := differ.HandlerFuncs{}
	d.AddFunc = func(vObj differ.ClusterObject) {
		if err := c.MultiClusterController.RequeueObject(vObj.OwnerCluster, vObj.Object); err != nil {
			klog.Errorf("error requeue vServiceAccount %s in cluster %s: %v", vObj.Key, vObj.GetOwnerCluster(), err)
		} else {
			metrics.CheckerRemedyStats.WithLabelValues("RequeuedTenantServiceAccounts").Inc()
		}
	}
	d.UpdateFunc = func(vObj, pObj differ.ClusterObject) {
		v := vObj.Object.(*corev1.ServiceAccount)
		p := pObj.Object.(*corev1.ServiceAccount)

		if p.Annotations[constants.LabelUID] != string(v.UID) {
			klog.Warningf("Found pServiceAccount %s delegated UID is different from tenant object", pObj.Key)
			d.OnDelete(pObj)
			return
		}

		// check if token exist
		if len(v.Secrets) == 0 {
			if err := c.MultiClusterController.RequeueObject(vObj.OwnerCluster, vObj.Object); err != nil {
				klog.Errorf("error requeue vServiceAccount %s in cluster %s: %v", vObj.Key, vObj.GetOwnerCluster(), err)
			} else {
				metrics.CheckerRemedyStats.WithLabelValues("RequeuedTenantServiceAccounts").Inc()
			}
		}
	}
	d.DeleteFunc = func(pObj differ.ClusterObject) {
		deleteOptions := &metav1.DeleteOptions{}
		deleteOptions.Preconditions = metav1.NewUIDPreconditions(string(pObj.GetUID()))
		if err = c.saClient.ServiceAccounts(pObj.GetNamespace()).Delete(context.TODO(), pObj.GetName(), *deleteOptions); err != nil {
			klog.Errorf("error deleting pServiceAccount %s in super control plane: %v", pObj.Key, err)
		} else {
			metrics.CheckerRemedyStats.WithLabelValues("DeletedOrphanSuperControlPlaneServiceAccounts").Inc()
		}
	}

	vSet.Difference(pSet, differ.FilteringHandler{
		Handler:    d,
		FilterFunc: differ.DefaultDifferFilter(knownClusterSet),
	})
}
