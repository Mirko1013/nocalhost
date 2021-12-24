/*
* Copyright (C) 2020 THL A29 Limited, a Tencent company.  All rights reserved.
* This source code is licensed under the Apache License Version 2.0.
 */

package vcluster

import (
	"encoding/base64"
	"sync"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"

	"nocalhost/internal/nocalhost-api/model"
	helmv1alpha1 "nocalhost/internal/nocalhost-dep/controllers/vcluster/api/v1alpha1"
	"nocalhost/pkg/nocalhost-api/pkg/clientgo"
)

const (
	defaultResync = 10 * time.Minute
)

type Manager interface {
	GetInfo(name, namespace string) (model.VirtualClusterInfo, error)
	GetKubeConfig(name, namespace string) (string, string, error)
	close()
}

type manager struct {
	mu        sync.Mutex
	client    *clientgo.GoClient
	informers dynamicinformer.DynamicSharedInformerFactory
	stopCh    chan struct{}
}

var _ Manager = &manager{}

func (m *manager) GetInfo(name, namespace string) (model.VirtualClusterInfo, error) {
	info := model.VirtualClusterInfo{}
	vc, err := m.getVirtualCluster(name, namespace)
	if err != nil {
		info.Status = string(helmv1alpha1.Unknown)
		return info, err
	}
	info.Status = string(vc.Status.Phase)
	info.Version = vc.GetChartVersion()
	info.Values = vc.GetValues()
	info.ServiceType = corev1.ServiceType(vc.GetServiceType())
	return info, nil
}

func (m *manager) GetKubeConfig(name, namespace string) (string, string, error) {
	vc, err := m.getVirtualCluster(name, namespace)
	if err != nil {
		return "", "", err
	}

	if vc.Status.Phase != helmv1alpha1.Ready {
		return "", "", errors.New("virtual cluster is not ready")
	}

	kubeConfig, err := base64.StdEncoding.DecodeString(vc.Status.AuthConfig)
	if err != nil {
		return "", "", err
	}
	serviceType := vc.GetServiceType()
	return string(kubeConfig), serviceType, nil
}

func (m *manager) vcInformer() informers.GenericInformer {
	m.mu.Lock()
	defer m.mu.Unlock()
	informer := m.informers.ForResource(schema.GroupVersionResource{
		Group:    "helm.nocalhost.dev",
		Version:  "v1alpha1",
		Resource: "virtualclusters",
	})
	m.informers.Start(m.stopCh)
	m.informers.WaitForCacheSync(m.stopCh)
	return informer
}

func (m *manager) getVirtualCluster(name, namespace string) (*helmv1alpha1.VirtualCluster, error) {
	informer := m.vcInformer()
	informer.Lister()
	obj, exists, err := informer.Informer().GetIndexer().GetByKey(namespace + "/" + name)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	if !exists {
		return nil, nil
	}
	vc := &helmv1alpha1.VirtualCluster{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(
		obj.(*unstructured.Unstructured).UnstructuredContent(), vc); err != nil {
		return nil, errors.WithStack(err)
	}
	return vc, nil
}

func (m *manager) getVirtualClusterList() (*helmv1alpha1.VirtualClusterList, error) {
	informer := m.vcInformer()
	informer.Lister()
	objs := informer.Informer().GetIndexer().List()
	vcList := &helmv1alpha1.VirtualClusterList{}
	for i := 0; i < len(objs); i++ {
		vc := &helmv1alpha1.VirtualCluster{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(
			objs[i].(*unstructured.Unstructured).UnstructuredContent(), vc); err != nil {
			return nil, errors.WithStack(err)
		}
		vcList.Items = append(vcList.Items, *vc)
	}
	return vcList, nil
}

func (m *manager) close() {
	close(m.stopCh)
}

func newManager(client *clientgo.GoClient) Manager {
	return &manager{
		client:    client,
		informers: dynamicinformer.NewDynamicSharedInformerFactory(client.DynamicClient, defaultResync),
	}
}