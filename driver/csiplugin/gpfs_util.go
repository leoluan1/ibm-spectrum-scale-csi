/**
 * Copyright 2019 IBM Corp.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package scale

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/IBM/ibm-spectrum-scale-csi/driver/csiplugin/connectors"
	"github.com/IBM/ibm-spectrum-scale-csi/driver/csiplugin/utils"
	"github.com/golang/glog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	dependentFileset   = "dependent"
	independentFileset = "independent"
)

type scaleVolume struct {
	VolName            string                            `json:"volName"`
	VolSize            uint64                            `json:"volSize"`
	VolBackendFs       string                            `json:"volBackendFs"`
	IsFilesetBased     bool                              `json:"isFilesetBased"`
	VolDirBasePath     string                            `json:"volDirBasePath"`
	VolUid             string                            `json:"volUid"`
	VolGid             string                            `json:"volGid"`
	VolPermissions     string                            `json:"volPermissions"`
	ClusterId          string                            `json:"clusterId"`
	FilesetType        string                            `json:"filesetType"`
	InodeLimit         string                            `json:"inodeLimit"`
	Connector          connectors.SpectrumScaleConnector `json:"connector"`
	PrimaryConnector   connectors.SpectrumScaleConnector `json:"primaryConnector"`
	PrimarySLnkRelPath string                            `json:"primarySLnkRelPath"`
	PrimarySLnkPath    string                            `json:"primarySLnkPath"`
	PrimaryFS          string                            `json:"primaryFS"`
	PrimaryFSMount     string                            `json:"primaryFSMount"`
	ParentFileset      string                            `json:"parentFileset"`
	LocalFS            string                            `json:"localFS"`
	TargetPath         string                            `json:"targetPath"`
	FsetLinkPath       string                            `json:"fsetLinkPath"`
	FsMountPoint       string                            `json:"fsMountPoint"`
	NodeClass          string                            `json:"nodeClass"`
}

type scaleVolId struct {
	ClusterId      string
	FsUUID         string
	FsName         string
	FsetId         string
	FsetName       string
	DirPath        string
	SymLnkPath     string
	IsFilesetBased bool
}

type scaleSnapId struct {
	ClusterId string
	FsUUID    string
	FsetName  string
	SnapName  string
	Path      string
	FsName    string
}

//nolint
type scaleVolSnapshot struct {
	SnapName   string `json:"snapName"`
	SourceVol  string `json:"sourceVol"`
	Filesystem string `json:"filesystem"`
	Fileset    string `json:"fileset"`
	ClusterId  string `json:"clusterId"`
	SnapSize   uint64 `json:"snapSize"`
} //nolint

//nolint
type scaleVolSnapId struct {
	ClusterId string
	FsUUID    string
	FsetId    string
	SnapId    string
} //nolint

func getRemoteFsName(remoteDeviceName string) string {
	splitDevName := strings.Split(remoteDeviceName, ":")
	remDevFs := splitDevName[len(splitDevName)-1]
	return remDevFs
}

func getScaleVolumeOptions(volOptions map[string]string) (*scaleVolume, error) { //nolint:gocyclo,funlen
	//var err error
	scaleVol := &scaleVolume{}

	volBckFs, fsSpecified := volOptions[connectors.UserSpecifiedVolBackendFs]
	volDirPath, volDirPathSpecified := volOptions[connectors.UserSpecifiedVolDirPath]
	clusterID, clusterIDSpecified := volOptions[connectors.UserSpecifiedClusterId]
	uid, uidSpecified := volOptions[connectors.UserSpecifiedUid]
	gid, gidSpecified := volOptions[connectors.UserSpecifiedGid]
	fsType, fsTypeSpecified := volOptions[connectors.UserSpecifiedFilesetType]
	inodeLim, inodeLimSpecified := volOptions[connectors.UserSpecifiedInodeLimit]
	parentFileset, isparentFilesetSpecified := volOptions[connectors.UserSpecifiedParentFset]
	nodeClass, isNodeClassSpecified := volOptions[connectors.UserSpecifiedNodeClass]
	permissions, isPermissionsSpecified := volOptions[connectors.UserSpecifiedPermissions]

	// Handling empty values
	scaleVol.VolDirBasePath = ""
	scaleVol.InodeLimit = ""
	scaleVol.FilesetType = ""
	scaleVol.ClusterId = ""
	scaleVol.NodeClass = ""

	if fsSpecified && volBckFs == "" {
		fsSpecified = false
	}

	if fsSpecified {
		scaleVol.VolBackendFs = volBckFs
	} else {
		return &scaleVolume{}, status.Error(codes.InvalidArgument, "volBackendFs must be specified in storageClass")
	}

	if fsTypeSpecified && fsType == "" {
		fsTypeSpecified = false
	}

	if volDirPathSpecified && volDirPath == "" {
		volDirPathSpecified = false
	}

	if !fsTypeSpecified && !volDirPathSpecified {
		fsTypeSpecified = true
		fsType = independentFileset
	}

	if uidSpecified && uid == "" {
		uidSpecified = false
	}

	if gidSpecified && gid == "" {
		gidSpecified = false
	}

	if gidSpecified && !uidSpecified {
		uidSpecified = true
		uid = "0"
	}

	if inodeLimSpecified && inodeLim == "" {
		inodeLimSpecified = false
	}

	if isparentFilesetSpecified && parentFileset == "" {
		isparentFilesetSpecified = false
	}
	if clusterIDSpecified && clusterID != "" {
		scaleVol.ClusterId = clusterID
	}

	if isPermissionsSpecified && permissions == "" {
		isPermissionsSpecified = false
	}

	if volDirPathSpecified {
		if fsTypeSpecified {
			return &scaleVolume{}, status.Error(codes.InvalidArgument, "filesetType and volDirBasePath must not be specified together in storageClass")
		}
		if isparentFilesetSpecified {
			return &scaleVolume{}, status.Error(codes.InvalidArgument, "parentFileset and volDirBasePath must not be specified together in storageClass")
		}
		if inodeLimSpecified {
			return &scaleVolume{}, status.Error(codes.InvalidArgument, "inodeLimit and volDirBasePath must not be specified together in storageClass")
		}
	}

	if fsTypeSpecified {
		if fsType == dependentFileset {
			if inodeLimSpecified {
				return &scaleVolume{}, status.Error(codes.InvalidArgument, "inodeLimit and filesetType=dependent must not be specified together in storageClass")
			}
		} else if fsType == independentFileset {
			if isparentFilesetSpecified {
				return &scaleVolume{}, status.Error(codes.InvalidArgument, "parentFileset and filesetType=independent(Default) must not be specified together in storageClass")
			}
		} else {
			return &scaleVolume{}, status.Error(codes.InvalidArgument, "Invalid value specified for filesetType in storageClass")
		}
	}

	if fsTypeSpecified && inodeLimSpecified {
		inodelimit, err := strconv.Atoi(inodeLim)
		if err != nil {
			return &scaleVolume{}, status.Error(codes.InvalidArgument, "Invalid value specified for inodeLimit in storageClass")
		}
		if inodelimit < 1024 {
			return &scaleVolume{}, status.Error(codes.InvalidArgument, "inodeLimit specified in storageClass must be equal to or greater than 1024")
		}
	}

	/* Check if either fileset based or LW volume. */

	if volDirPathSpecified {
		scaleVol.VolDirBasePath = volDirPath
		scaleVol.IsFilesetBased = false
	}
	if fsTypeSpecified {
		scaleVol.IsFilesetBased = true
	}

	/* Get UID/GID */
	if uidSpecified {
		scaleVol.VolUid = uid
	}

	if gidSpecified {
		scaleVol.VolGid = gid
	}

	if isPermissionsSpecified {
		_, err := strconv.Atoi(permissions)
		if err != nil || len(permissions) != 3 {
			return &scaleVolume{}, status.Error(codes.InvalidArgument, "invalid value specified for permissions")
		}

		for _, n := range permissions {
			if n < 48 || n > 55 {
				return &scaleVolume{}, status.Error(codes.InvalidArgument, "invalid value specified for permissions")
			}
		}

		scaleVol.VolPermissions = permissions
	}

	if scaleVol.IsFilesetBased {
		if fsTypeSpecified {
			scaleVol.FilesetType = fsType
		}
		if isparentFilesetSpecified {
			scaleVol.ParentFileset = parentFileset
		}
		if inodeLimSpecified {
			scaleVol.InodeLimit = inodeLim
		}
	}

	if isNodeClassSpecified {
		scaleVol.NodeClass = nodeClass
	}

	return scaleVol, nil
}

func executeCmd(command string, args []string) ([]byte, error) {
	glog.V(5).Infof("gpfs_util executeCmd")

	cmd := exec.Command(command, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	stdOut := stdout.Bytes()
	return stdOut, err
}

func ConvertToBytes(inputStr string) (uint64, error) {
	var Iter int
	var byteSlice []byte
	var retValue uint64
	var uintMax64 uint64

	byteSlice = []byte(inputStr)
	uintMax64 = (1 << 64) - 1

	for Iter = 0; Iter < len(byteSlice); Iter++ {
		if ('0' <= byteSlice[Iter]) &&
			(byteSlice[Iter] <= '9') {
			continue
		} else {
			break
		}
	}
	if Iter == 0 {
		return 0, fmt.Errorf("Invalid number specified %v", inputStr)
	}

	retValue, err := strconv.ParseUint(inputStr[:Iter], 10, 64)

	if err != nil {
		return 0, fmt.Errorf("ParseUint Failed for %v", inputStr[:Iter])
	}

	if Iter == len(inputStr) {
		return retValue, nil
	}

	unit := strings.TrimSpace(string(byteSlice[Iter:]))
	unit = strings.ToLower(unit)

	switch unit {
	case "b", "bytes":
		/* Nothing to do here */
	case "k", "kb", "kilobytes", "kilobyte":
		retValue *= 1024
	case "m", "mb", "megabytes", "megabyte":
		retValue *= (1024 * 1024)
	case "g", "gb", "gigabytes", "gigabyte":
		retValue *= (1024 * 1024 * 1024)
	case "t", "tb", "terabytes", "terabyte":
		retValue *= (1024 * 1024 * 1024 * 1024)
	default:
		return 0, fmt.Errorf("Invalid Unit %v supplied with %v", unit, inputStr)
	}

	if retValue > uintMax64 {
		return 0, fmt.Errorf("Overflow detected %v", inputStr)
	}

	return retValue, nil
}

const (
	SCALE_NODE_MAPPING_PREFIX = "SCALE_NODE_MAPPING_PREFIX"
	DefaultScaleNodeMapPrefix = "K8sNodePrefix_"
)

// getNodeMapping returns the configured mapping to GPFS Admin Node Name given Kubernetes Node ID.
func getNodeMapping(kubernetesNodeID string) (gpfsAdminName string) {
	gpfsAdminName = utils.GetEnv(kubernetesNodeID, notFound)
	// Additional node mapping check in case of k8s node id start with number.
	if gpfsAdminName == notFound {
		prefix := utils.GetEnv(SCALE_NODE_MAPPING_PREFIX, DefaultScaleNodeMapPrefix)
		gpfsAdminName = utils.GetEnv(prefix+kubernetesNodeID, notFound)
		if gpfsAdminName == notFound {
			glog.V(4).Infof("getNodeMapping: scale node mapping not found for %s using %s", prefix+kubernetesNodeID, kubernetesNodeID)
			gpfsAdminName = kubernetesNodeID
		}
	}
	return gpfsAdminName
}

const (
	SHORTNAME_NODE_MAPPING = "SHORTNAME_NODE_MAPPING"
	SKIP_MOUNT_UNMOUNT     = "SKIP_MOUNT_UNMOUNT"
)

func shortnameInSlice(shortname string, nodeNames []string) bool {
	glog.V(6).Infof("gpfs_util shortnameInSlice. string: %s, slice: %v", shortname, nodeNames)
	for _, name := range nodeNames {
		short := strings.SplitN(name, ".", 2)[0]
		if strings.EqualFold(short, shortname) {
			return true
		}
	}
	return false
}

func numberInSlice(a int, list []int) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}
