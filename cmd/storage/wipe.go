package storage

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"

	"git.f-i-ts.de/cloud-native/metal/metal-hammer/pkg/os"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/pkg/password"

	log "github.com/inconshreveable/log15"
	"github.com/jaypipes/ghw"
	"github.com/pkg/errors"
)

var (
	hdparmCommand = "hdparm"
	nvmeCommand   = "nvme"
)

// WipeDisks will erase all content and partitions of all existing Disks
func WipeDisks() error {
	log.Info("wipe")
	block, err := ghw.Block()
	if err != nil {
		return errors.Wrap(err, "unable to gather disks")
	}
	disks := block.Disks

	log.Info("wipe existing disks", "disks", disks)

	for _, disk := range disks {
		device := fmt.Sprintf("/dev/%s", disk.Name)
		bytes := disk.SizeBytes

		err := wipe(device, bytes)
		if err != nil {
			log.Error("wipe", "unable to wipe", "disk", disk.Name, "error", err)
		}
	}

	return nil
}

// bs is the blocksize in bytes to be used by dd
const bs = uint64(10240)

func wipe(device string, bytes uint64) error {
	if isSEDAvailable(device) {
		return secureErase(device)
	} else if isNVMeDisk(device) {
		return secureEraseNVMe(device)
	}
	err := discard(device)
	if err != nil {
		return wipeSlow(device, bytes)
	}
	return nil
}

func discard(device string) error {
	log.Info("wipe", "disk", device, "message", "discard existing data")
	err := os.ExecuteCommand("mkfs.ext4", "-F", "-E", "discard", device)
	if err != nil {
		log.Error("wipe", "disk", device, "message", "discard of existing data failed", "error", err)
		return err
	}
	log.Info("wipe", "disk", device, "message", "finish discard of existing data")
	return nil
}

func wipeSlow(device string, bytes uint64) error {
	log.Info("wipe", "disk", device, "message", "slow deleting of existing data")
	count := bytes / bs
	bsArg := fmt.Sprintf("bs=%d", bs)
	countArg := fmt.Sprintf("count=%d", count)
	err := os.ExecuteCommand("/bbin/dd", "status=progress", "if=/dev/zero", "of="+device, bsArg, countArg)
	if err != nil {
		log.Error("wipe", "disk", device, "message", "overwrite of existing data with dd failed", "error", err)
		return err
	}
	log.Info("wipe", "disk", device, "message", "finish deleting of existing data")
	return nil
}

// isSEDAvailable check the disk if it is a Self Encryption Device
// check with hdparm -I for Self Encrypting Device, sample output will look like:
// Security:
//         Master password revision code = 65534
//                 supported
//         not     enabled
//         not     locked
//                 frozen
//         not     expired: security count
//                 supported: enhanced erase
//         6min for SECURITY ERASE UNIT. 32min for ENHANCED SECURITY ERASE UNIT.
// explanation is here: https://wiki.ubuntuusers.de/SSD/Secure-Erase/
func isSEDAvailable(device string) bool {
	path, err := exec.LookPath(hdparmCommand)
	if err != nil {
		log.Error("unable to locate", "command", hdparmCommand, "error", err)
		return false
	}
	cmd := exec.Command(path, "-I", device)
	output, err := cmd.Output()
	if err != nil {
		log.Error("error executing hdparm", "error", err)
		return false
	}
	hdparmOutput := string(output)
	if strings.Contains(hdparmOutput, "supported: enhanced erase") {
		scanner := bufio.NewScanner(strings.NewReader(hdparmOutput))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "frozen") && !strings.Contains(line, "not") {
				log.Info("wipe", "message", "sed is not available, disk is frozen")
				return false
			}
			if strings.Contains(line, "supported: enhanced erase") && strings.Contains(line, "not") {
				log.Info("wipe", "message", "sed is not available, enhanced erase is not supported")
				return false
			}
		}
		log.Info("wipe", "message", "sed is available")
		return true
	}
	log.Info("wipe", "message", "sed is not available, enhanced erase is not supported")
	return false
}

func isNVMeDisk(device string) bool {
	if strings.HasPrefix(device, "/dev/nvm") {
		return true
	}
	return false
}

// Secure erase is done via:
// nvme-cli --format --ses=1 /dev/nvme0n1
// see: https://github.com/linux-nvme/nvme-cli/blob/master/Documentation/nvme-format.txt
//
// TODO: configure qemu to map a disk with the nvme format:
// https://github.com/nvmecompliance/manage/blob/master/runQemu.sh
// https://github.com/arunar/nvmeqemu
func secureEraseNVMe(device string) error {
	log.Info("wipe", "disk", device, "message", "start very fast deleting of existing data")
	err := os.ExecuteCommand(nvmeCommand, "--format", "--ses=1", device)
	if err != nil {
		return errors.Wrapf(err, "unable to secure erase nvme disk %s", device)
	}
	return nil
}

func secureErase(device string) error {
	log.Info("wipe", "disk", device, "message", "start fast deleting of existing data")
	// hdparm --user-master u --security-set-pass GEHEIM /dev/sda
	// FIXME random password
	pw := password.Generate(10)
	// first we must set a secure erase password
	err := os.ExecuteCommand(hdparmCommand, "--user-master", "u", "--security-set-pass", pw, device)
	if err != nil {
		return errors.Wrapf(err, "unable to set secure erase password disk: %s", device)
	}
	// now we can start secure erase
	err = os.ExecuteCommand(hdparmCommand, "--user-master", "u", "--security-erase", pw, device)
	if err != nil {
		return errors.Wrapf(err, "unable to secure erase disk: %s", device)
	}
	return nil
}
