// Package openstack generates Machine objects for openstack.
package openstack

import (
	"context"
	"fmt"

	"github.com/gophercloud/utils/openstack/clientconfig"
	clusterapi "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift/installer/pkg/asset/installconfig"
	"github.com/openshift/installer/pkg/rhcos"
	"github.com/openshift/installer/pkg/types"
	"github.com/openshift/installer/pkg/types/openstack"
	openstackdefaults "github.com/openshift/installer/pkg/types/openstack/defaults"
)

func MachineSets(
	_ context.Context,
	installConfig *installconfig.InstallConfig,
	clusterID *installconfig.ClusterID,
	rhcosImage string,
	pool types.MachinePool,
	role string,
	userDataSecret string,
	clientOpts *clientconfig.ClientOpts,
) ([]*clusterapi.MachineSet, error) {
	mpool := defaultOpenStackMachinePoolPlatform()
	mpool.Set(installConfig.Config.Platform.OpenStack.DefaultMachinePlatform)
	mpool.Set(pool.Platform.OpenStack)
	pool.Platform.OpenStack = &mpool

	imageName, _ := rhcos.GenerateOpenStackImageName(rhcosImage, clusterID.InfraID)

	return machineSets(clusterID.InfraID, installConfig.Config, &pool, imageName, "worker", "worker-user-data", nil)
}

// MachineSets returns a list of machinesets for a machinepool.
func machineSets(clusterID string, config *types.InstallConfig, pool *types.MachinePool, osImage, role, userDataSecret string, clientOpts *clientconfig.ClientOpts) ([]*clusterapi.MachineSet, error) {
	if configPlatform := config.Platform.Name(); configPlatform != openstack.Name {
		return nil, fmt.Errorf("non-OpenStack configuration: %q", configPlatform)
	}
	if poolPlatform := pool.Platform.Name(); poolPlatform != openstack.Name {
		return nil, fmt.Errorf("non-OpenStack machine-pool: %q", poolPlatform)
	}
	platform := config.Platform.OpenStack
	mpool := pool.Platform.OpenStack
	trunkSupport, err := checkNetworkExtensionAvailability(platform.Cloud, "trunk", clientOpts)
	if err != nil {
		return nil, err
	}

	volumeAZs := openstackdefaults.DefaultRootVolumeAZ()
	if mpool.RootVolume != nil && len(mpool.RootVolume.Zones) != 0 {
		volumeAZs = mpool.RootVolume.Zones
	}

	total := int32(0)
	if pool.Replicas != nil {
		total = int32(*pool.Replicas)
	}

	numOfAZs := int32(len(mpool.Zones))
	var machinesets []*clusterapi.MachineSet

	for idx, az := range mpool.Zones {
		replicas := int32(total / numOfAZs)
		if int32(idx) < total%numOfAZs {
			replicas++
		}
		provider, err := generateProvider(clusterID, platform, mpool, osImage, az, role, userDataSecret, trunkSupport, volumeAZs[idx%len(volumeAZs)])
		if err != nil {
			return nil, err
		}

		// Set unique name for the machineset
		name := fmt.Sprintf("%s-%s-%d", clusterID, pool.Name, idx)

		mset := &clusterapi.MachineSet{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "machine.openshift.io/v1beta1",
				Kind:       "MachineSet",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "openshift-machine-api",
				Name:      name,
				Labels: map[string]string{
					"machine.openshift.io/cluster-api-cluster":      clusterID,
					"machine.openshift.io/cluster-api-machine-role": role,
					"machine.openshift.io/cluster-api-machine-type": role,
				},
			},
			Spec: clusterapi.MachineSetSpec{
				Replicas: &replicas,
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"machine.openshift.io/cluster-api-machineset": name,
						"machine.openshift.io/cluster-api-cluster":    clusterID,
					},
				},
				Template: clusterapi.MachineTemplateSpec{
					ObjectMeta: clusterapi.ObjectMeta{
						Labels: map[string]string{
							"machine.openshift.io/cluster-api-machineset":   name,
							"machine.openshift.io/cluster-api-cluster":      clusterID,
							"machine.openshift.io/cluster-api-machine-role": role,
							"machine.openshift.io/cluster-api-machine-type": role,
						},
					},
					Spec: clusterapi.MachineSpec{
						ProviderSpec: clusterapi.ProviderSpec{
							Value: &runtime.RawExtension{Object: provider},
						},
						// we don't need to set Versions, because we control those via cluster operators.
					},
				},
			},
		}
		machinesets = append(machinesets, mset)
	}

	return machinesets, nil
}

func defaultOpenStackMachinePoolPlatform() openstack.MachinePool {
	return openstack.MachinePool{
		Zones: []string{""},
	}
}
