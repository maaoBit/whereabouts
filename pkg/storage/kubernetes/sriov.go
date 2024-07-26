package kubernetes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/k8snetworkplumbingwg/sriovnet"
	nl "github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
)

var (
	sysBusPci = "/sys/bus/pci/devices"
)

const (
	eswitchModeSwitchdev = "switchdev"
)

// GetPfName returns SRIOV PF name for the given VF
// If device is not VF then it will return empty string
func GetPfName(pciAddr string) (string, error) {
	if !IsSriovVF(pciAddr) {
		return "", nil
	}

	pfEswitchMode, err := GetPfEswitchMode(pciAddr)
	if pfEswitchMode == "" {
		// If device doesn't support eswitch mode query or doesn't have sriov enabled,
		// fall back to the default implementation
		if err == nil || strings.Contains(strings.ToLower(err.Error()), "error getting devlink device attributes for net device") {
			klog.Infof("Devlink query for eswitch mode is not supported for device %s. %v", pciAddr, err)
		} else {
			return "", err
		}
	} else if pfEswitchMode == eswitchModeSwitchdev {
		name, err := sriovnet.GetUplinkRepresentor(pciAddr)
		if err != nil {
			return "", err
		}

		return name, nil
	}

	path := filepath.Join(sysBusPci, pciAddr, "physfn", "net")
	files, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	} else if len(files) > 0 {
		return files[0].Name(), nil
	}
	return "", fmt.Errorf("the PF name is not found for device %s", pciAddr)
}

// IsSriovVF check if a pci device has link to a PF
func IsSriovVF(pciAddr string) bool {
	totalVfFilePath := filepath.Join(sysBusPci, pciAddr, "physfn")
	if _, err := os.Stat(totalVfFilePath); err != nil {
		return false
	}
	return true
}

// GetPfEswitchMode returns PF's eswitch mode for the given VF
// If device is not VF then it will return its own eswitch mode
func GetPfEswitchMode(pciAddr string) (string, error) {
	pfAddr, err := GetPfAddr(pciAddr)
	if err != nil {
		return "", fmt.Errorf("error getting PF PCI address for device %s %v", pciAddr, err)
	}
	devLinkDeviceAttrs, err := GetDevLinkDeviceEswitchAttrs(pfAddr)
	if err != nil {
		return "", err
	}
	return devLinkDeviceAttrs.Mode, nil
}

// GetPfAddr returns SRIOV PF pci address if a device is VF given its pci address.
// If device it not VF then it will return empty string
func GetPfAddr(pciAddr string) (string, error) {
	pfSymLink := filepath.Join(sysBusPci, pciAddr, "physfn")
	pciinfo, err := os.Readlink(pfSymLink)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("error getting PF for PCI device %s %v", pciAddr, err)
	}
	return filepath.Base(pciinfo), nil
}

// GetDevLinkDeviceEswitchAttrs returns a devlink device's attributes
func GetDevLinkDeviceEswitchAttrs(pfAddr string) (*nl.DevlinkDevEswitchAttr, error) {
	dev, err := nl.DevLinkGetDeviceByName("pci", pfAddr)
	if err != nil {
		return nil, fmt.Errorf("error getting devlink device attributes for net device %s %v", pfAddr, err)
	}
	return &(dev.Attrs.Eswitch), nil
}
