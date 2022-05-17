package kube

import (
	"os"

	"github.com/containers/common/pkg/parse"
	"github.com/containers/podman/v4/libpod"
	v1 "github.com/containers/podman/v4/pkg/k8s.io/api/core/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	// https://kubernetes.io/docs/concepts/storage/volumes/#hostpath
	kubeDirectoryPermission = 0755
	// https://kubernetes.io/docs/concepts/storage/volumes/#hostpath
	kubeFilePermission = 0644
)

//nolint:revive
type KubeVolumeType int

const (
	KubeVolumeTypeBindMount KubeVolumeType = iota
	KubeVolumeTypeNamed
	KubeVolumeTypeConfigMap
	KubeVolumeTypeBlockDevice
	KubeVolumeTypeCharDevice
)

//nolint:revive
type KubeVolume struct {
	// Type of volume to create
	Type KubeVolumeType
	// Path for bind mount or volume name for named volume
	Source string
	// Items to add to a named volume created where the key is the file name and the value is the data
	// This is only used when there are volumes in the yaml that refer to a configmap
	// Example: if configmap has data "SPECIAL_LEVEL: very" then the file name is "SPECIAL_LEVEL" and the
	// data in that file is "very".
	Items map[string]string
	// If the volume is optional, we can move on if it is not found
	// Only used when there are volumes in a yaml that refer to a configmap
	Optional bool
}

// Create a KubeVolume from an HostPathVolumeSource
func VolumeFromHostPath(hostPath *v1.HostPathVolumeSource) (*KubeVolume, error) {
	if hostPath.Type != nil {
		switch *hostPath.Type {
		case v1.HostPathDirectoryOrCreate:
			if _, err := os.Stat(hostPath.Path); os.IsNotExist(err) {
				if err := os.Mkdir(hostPath.Path, kubeDirectoryPermission); err != nil {
					return nil, err
				}
			}
			// Label a newly created volume
			if err := libpod.LabelVolumePath(hostPath.Path); err != nil {
				return nil, errors.Wrapf(err, "error giving %s a label", hostPath.Path)
			}
		case v1.HostPathFileOrCreate:
			if _, err := os.Stat(hostPath.Path); os.IsNotExist(err) {
				f, err := os.OpenFile(hostPath.Path, os.O_RDONLY|os.O_CREATE, kubeFilePermission)
				if err != nil {
					return nil, errors.Wrap(err, "error creating HostPath")
				}
				if err := f.Close(); err != nil {
					logrus.Warnf("Error in closing newly created HostPath file: %v", err)
				}
			}
			// unconditionally label a newly created volume
			if err := libpod.LabelVolumePath(hostPath.Path); err != nil {
				return nil, errors.Wrapf(err, "error giving %s a label", hostPath.Path)
			}
		case v1.HostPathSocket:
			st, err := os.Stat(hostPath.Path)
			if err != nil {
				return nil, errors.Wrap(err, "error checking HostPathSocket")
			}
			if st.Mode()&os.ModeSocket != os.ModeSocket {
				return nil, errors.Errorf("checking HostPathSocket: path %s is not a socket", hostPath.Path)
			}
		case v1.HostPathBlockDev:
			dev, err := os.Stat(hostPath.Path)
			if err != nil {
				return nil, errors.Wrap(err, "error checking HostPathBlockDevice")
			}
			if dev.Mode()&os.ModeDevice != os.ModeDevice {
				return nil, errors.Errorf("checking HosPathDevice: path %s is not a block device", hostPath.Path)
			}
			return &KubeVolume{
				Type:   KubeVolumeTypeBlockDevice,
				Source: hostPath.Path,
			}, nil
		case v1.HostPathCharDev:
			dev, err := os.Stat(hostPath.Path)
			if err != nil {
				return nil, errors.Wrap(err, "error checking HostPathCharDevice")
			}
			if dev.Mode()&os.ModeCharDevice != os.ModeCharDevice {
				return nil, errors.Errorf("checking HosPathCharDevice: path %s is not a character device", hostPath.Path)
			}
			return &KubeVolume{
				Type:   KubeVolumeTypeCharDevice,
				Source: hostPath.Path,
			}, nil
		case v1.HostPathDirectory:
		case v1.HostPathFile:
		case v1.HostPathUnset:
			// do nothing here because we will verify the path exists in validateVolumeHostDir
			break
		default:
			return nil, errors.Errorf("Invalid HostPath type %v", hostPath.Type)
		}
	}

	if err := parse.ValidateVolumeHostDir(hostPath.Path); err != nil {
		return nil, errors.Wrapf(err, "error in parsing HostPath in YAML")
	}

	return &KubeVolume{
		Type:   KubeVolumeTypeBindMount,
		Source: hostPath.Path,
	}, nil
}

// Create a KubeVolume from a PersistentVolumeClaimVolumeSource
func VolumeFromPersistentVolumeClaim(claim *v1.PersistentVolumeClaimVolumeSource) (*KubeVolume, error) {
	return &KubeVolume{
		Type:   KubeVolumeTypeNamed,
		Source: claim.ClaimName,
	}, nil
}

func VolumeFromConfigMap(configMapVolumeSource *v1.ConfigMapVolumeSource, configMaps []v1.ConfigMap) (*KubeVolume, error) {
	var configMap *v1.ConfigMap
	kv := &KubeVolume{Type: KubeVolumeTypeConfigMap, Items: map[string]string{}}
	for _, cm := range configMaps {
		if cm.Name == configMapVolumeSource.Name {
			matchedCM := cm
			// Set the source to the config map name
			kv.Source = cm.Name
			configMap = &matchedCM
			break
		}
	}

	if configMap == nil {
		// If the volumeSource was optional, move on even if a matching configmap wasn't found
		if configMapVolumeSource.Optional != nil && *configMapVolumeSource.Optional {
			kv.Source = configMapVolumeSource.Name
			kv.Optional = *configMapVolumeSource.Optional
			return kv, nil
		}
		return nil, errors.Errorf("no such ConfigMap %q", configMapVolumeSource.Name)
	}

	// If there are Items specified in the volumeSource, that overwrites the Data from the configmap
	if len(configMapVolumeSource.Items) > 0 {
		for _, item := range configMapVolumeSource.Items {
			if val, ok := configMap.Data[item.Key]; ok {
				kv.Items[item.Path] = val
			}
		}
	} else {
		for k, v := range configMap.Data {
			kv.Items[k] = v
		}
	}
	return kv, nil
}

// Create a KubeVolume from one of the supported VolumeSource
func VolumeFromSource(volumeSource v1.VolumeSource, configMaps []v1.ConfigMap) (*KubeVolume, error) {
	switch {
	case volumeSource.HostPath != nil:
		return VolumeFromHostPath(volumeSource.HostPath)
	case volumeSource.PersistentVolumeClaim != nil:
		return VolumeFromPersistentVolumeClaim(volumeSource.PersistentVolumeClaim)
	case volumeSource.ConfigMap != nil:
		return VolumeFromConfigMap(volumeSource.ConfigMap, configMaps)
	default:
		return nil, errors.Errorf("HostPath, ConfigMap, and PersistentVolumeClaim are currently the only supported VolumeSource")
	}
}

// Create a map of volume name to KubeVolume
func InitializeVolumes(specVolumes []v1.Volume, configMaps []v1.ConfigMap) (map[string]*KubeVolume, error) {
	volumes := make(map[string]*KubeVolume)

	for _, specVolume := range specVolumes {
		volume, err := VolumeFromSource(specVolume.VolumeSource, configMaps)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create volume %q", specVolume.Name)
		}

		volumes[specVolume.Name] = volume
	}

	return volumes, nil
}
