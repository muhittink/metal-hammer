package cmd

import (
	"time"

	"git.f-i-ts.de/cloud-native/metal/metal-hammer/metal-core/client/device"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/metal-core/models"

	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/network"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/register"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/report"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/storage"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/pkg"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/pkg/password"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	log "github.com/inconshreveable/log15"
	"github.com/pkg/errors"
)

// Hammer is the machine which forms a bare metal to a working server
type Hammer struct {
	Client     *device.Client
	Spec       *Specification
	Disk       storage.Disk
	LLDPClient *network.LLDPClient
	// IPAddress is the ip of the eth0 interface during installation
	IPAddress string
	Started   time.Time
}

// Run orchestrates the whole register/wipe/format/burn and reboot process
func Run(spec *Specification) error {
	log.Info("metal-hammer run", "firmware", pkg.Firmware())

	transport := httptransport.New(spec.MetalCoreURL, "", nil)
	client := device.New(transport, strfmt.Default)

	hammer := &Hammer{
		Client:    client,
		Spec:      spec,
		IPAddress: spec.Ip,
	}
	hammer.Spec.ConsolePassword = password.Generate(16)

	n := &network.Network{
		DeviceUUID: spec.DeviceUUID,
		IPAddress:  spec.Ip,
		Started:    time.Now(),
	}

	err := n.UpAllInterfaces()
	if err != nil {
		return errors.Wrap(err, "interfaces")
	}

	err = hammer.EnsureUEFI()
	if err != nil {
		return errors.Wrap(err, "uefi")
	}

	err = storage.WipeDisks()
	if err != nil {
		return errors.Wrap(err, "wipe")
	}

	reg := &register.Register{
		DeviceUUID: spec.DeviceUUID,
		Client:     client,
		Network:    n,
	}

	// Remove uuid return use DeviceUUID() above.
	uuid, err := reg.RegisterDevice()
	if !spec.DevMode && err != nil {
		return errors.Wrap(err, "register")
	}

	// Ensure we can run without metal-core, given IMAGE_URL is configured as kernel cmdline
	var deviceWithToken *models.ModelsMetalDeviceWithPhoneHomeToken
	if spec.DevMode {
		cidr := "10.0.1.2/24"
		if spec.Cidr != "" {
			cidr = spec.Cidr
		}

		if !spec.BGPEnabled {
			cidr = "dhcp"
		}
		hostname := "devmode"
		sshkeys := []string{"not a valid ssh public key, can be specified during device create.", "second public key"}
		fakeToken := "JWT"
		deviceWithToken = &models.ModelsMetalDeviceWithPhoneHomeToken{
			Device: &models.ModelsMetalDevice{
				Allocation: &models.ModelsMetalDeviceAllocation{
					Image: &models.ModelsMetalImage{
						URL: &spec.ImageURL,
						ID:  &spec.ImageID,
					},
					Hostname:   &hostname,
					SSHPubKeys: sshkeys,
					Cidr:       &cidr,
				},
			},
			PhoneHomeToken: &fakeToken,
		}
	} else {
		deviceWithToken, err = hammer.Wait(uuid)
		if err != nil {
			return errors.Wrap(err, "wait for installation")
		}
	}

	hammer.Disk = storage.GetDisk(deviceWithToken.Device.Allocation.Image)

	installationStart := time.Now()
	info, err := hammer.Install(deviceWithToken)

	// FIXME, must not return here.
	if err != nil {
		return errors.Wrap(err, "install")
	}

	rep := &report.Report{
		DeviceUUID:      spec.DeviceUUID,
		Client:          client,
		ConsolePassword: spec.ConsolePassword,
		InstallError:    err,
	}

	err = rep.ReportInstallation()
	if err != nil {
		wait := 10 * time.Second
		log.Error("report installation failed", "reboot in", wait, "error", err)
		time.Sleep(wait)
		if !spec.DevMode {
			err = pkg.Reboot()
			if err != nil {
				log.Error("reboot", "error", err)
			}
		}
	}

	log.Info("installation", "took", time.Since(installationStart))
	return pkg.RunKexec(info)
}
