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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	vcclient "sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/client/clientset/versioned"
	vcinformers "sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/client/informers/externalversions/tenancy/v1alpha1"
	"sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/syncer/apis/config"
	"sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/syncer/manager"
	pa "sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/syncer/patrol"
	"sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/syncer/resources/serviceaccount/token"
	mc "sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/util/mccontroller"
	"sigs.k8s.io/cluster-api-provider-nested/virtualcluster/pkg/util/plugin"
)

func init() {
	plugin.SyncerResourceRegister.Register(&plugin.Registration{
		ID: "serviceaccount",
		InitFn: func(ctx *plugin.InitContext) (interface{}, error) {
			return NewServiceAccountController(ctx.Config.(*config.SyncerConfiguration), ctx.Client, ctx.Informer, ctx.VCClient, ctx.VCInformer, manager.ResourceSyncerOptions{})
		},
	})
}

type controller struct {
	manager.BaseResourceSyncer
	// super control plane sa client
	saClient v1core.CoreV1Interface
	// super control plane sa lister/synced function
	saLister     listersv1.ServiceAccountLister
	saSynced     cache.InformerSynced
	tokenManager *token.Manager
}

func NewServiceAccountController(config *config.SyncerConfiguration,
	client clientset.Interface,
	informer informers.SharedInformerFactory,
	vcClient vcclient.Interface,
	vcInformer vcinformers.VirtualClusterInformer,
	options manager.ResourceSyncerOptions) (manager.ResourceSyncer, error) {
	c := &controller{
		saClient: client.CoreV1(),
	}

	var err error
	c.MultiClusterController, err = mc.NewMCController(&corev1.ServiceAccount{}, &corev1.ServiceAccountList{}, c, mc.WithOptions(options.MCOptions))
	if err != nil {
		return nil, err
	}

	c.saLister = informer.Core().V1().ServiceAccounts().Lister()
	if options.IsFake {
		c.saSynced = func() bool { return true }
	} else {
		c.saSynced = informer.Core().V1().ServiceAccounts().Informer().HasSynced
	}

	c.Patroller, err = pa.NewPatroller(&corev1.ServiceAccount{}, c, pa.WithOptions(options.PatrolOptions))
	if err != nil {
		return nil, err
	}

	return c, nil
}
