package cmd

import (
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/event"
	"time"

	"git.f-i-ts.de/cloud-native/metal/metal-hammer/metal-core/client/machine"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/metal-core/models"

	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/firmware"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/network"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/register"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/report"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/cmd/storage"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/pkg/kernel"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/pkg/password"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	log "github.com/inconshreveable/log15"
	"github.com/pkg/errors"
)

// Hammer is the machine which forms a bare metal to a working server
type Hammer struct {
	Client     *machine.Client
	Spec       *Specification
	Disk       storage.Disk
	LLDPClient *network.LLDPClient
	// IPAddress is the ip of the eth0 interface during installation
	IPAddress    string
	Started      time.Time
	EventEmitter *event.EventEmitter
}

// Run orchestrates the whole register/wipe/format/burn and reboot process
func Run(spec *Specification) (*event.EventEmitter, error) {
	log.Info("metal-hammer run", "firmware", kernel.Firmware())

	transport := httptransport.New(spec.MetalCoreURL, "", nil)
	client := machine.New(transport, strfmt.Default)
	eventEmitter := event.NewEventEmitter(client, spec.MachineUUID)

	eventEmitter.Emit(event.ProvisioningEventPreparing, "starting metal-hammer")

	hammer := &Hammer{
		Client:       client,
		Spec:         spec,
		IPAddress:    spec.IP,
		EventEmitter: eventEmitter,
	}

	// Reboot after 24Hours if no allocation was requested.
	go kernel.AutoReboot(24*time.Hour, func() {
		eventEmitter.Emit(event.ProvisioningEventPlannedReboot, "autoreboot after 24h")
	})

	hammer.Spec.ConsolePassword = password.Generate(16)

	n := &network.Network{
		MachineUUID: spec.MachineUUID,
		IPAddress:   spec.IP,
		Started:     time.Now(),
	}

	firmware := firmware.New()
	firmware.Update()

	lsi := storage.NewStorcli()
	err := lsi.EnableJBOD()
	if err != nil {
		log.Warn("root", "unable to format raid controller", err)
	}

	err = n.UpAllInterfaces()
	if err != nil {
		return eventEmitter, errors.Wrap(err, "interfaces")
	}

	// Set Time from ntp
	network.NtpDate()

	err = hammer.EnsureUEFI()
	if err != nil {
		return eventEmitter, errors.Wrap(err, "uefi")
	}

	err = storage.WipeDisks()
	if err != nil {
		return eventEmitter, errors.Wrap(err, "wipe")
	}

	reg := &register.Register{
		MachineUUID: spec.MachineUUID,
		Client:      client,
		Network:     n,
	}

	eventEmitter.Emit(event.ProvisioningEventRegistering, "start registering")
	// Remove uuid return use MachineUUID() above.
	uuid, err := reg.RegisterMachine()
	if !spec.DevMode && err != nil {
		return eventEmitter, errors.Wrap(err, "register")
	}
	eventEmitter.Emit(event.ProvisioningEventWaiting, "waiting for installation")

	// Ensure we can run without metal-core, given IMAGE_URL is configured as kernel cmdline
	var machineWithToken *models.ModelsMetalMachineWithPhoneHomeToken
	if spec.DevMode {
		cidr := "10.0.1.2/24"
		if spec.Cidr != "" {
			cidr = spec.Cidr
		}

		if !spec.BGPEnabled {
			cidr = "dhcp"
		}
		hostname := "devmode"
		sshkeys := []string{"not a valid ssh public key, can be specified during machine create.", "second public key"}
		fakeToken := "JWT"
		machineWithToken = &models.ModelsMetalMachineWithPhoneHomeToken{
			Machine: &models.ModelsMetalMachine{
				Allocation: &models.ModelsMetalMachineAllocation{
					Image: &models.ModelsMetalImage{
						URL: &spec.ImageURL,
						ID:  &spec.ImageID,
					},
					Hostname:   &hostname,
					SSHPubKeys: sshkeys,
					Cidr:       &cidr,
				},
				Size: &models.ModelsMetalSize{
					ID: &spec.SizeID,
				},
			},
			PhoneHomeToken: &fakeToken,
		}
	} else {
		machineWithToken, err = hammer.Wait(uuid)
		if err != nil {
			return eventEmitter, errors.Wrap(err, "wait for installation")
		}
	}

	hammer.Disk = storage.GetDisk(machineWithToken.Machine.Allocation.Image, machineWithToken.Machine.Size)

	eventEmitter.Emit(event.ProvisioningEventInstalling, "start installation")
	installationStart := time.Now()
	info, err := hammer.Install(machineWithToken)

	// FIXME, must not return here.
	if err != nil {
		return eventEmitter, errors.Wrap(err, "install")
	}

	rep := &report.Report{
		MachineUUID:     spec.MachineUUID,
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
			err = kernel.Reboot()
			if err != nil {
				log.Error("reboot", "error", err)
			}
		}
	}

	log.Info("installation", "took", time.Since(installationStart))
	eventEmitter.Emit(event.ProvisioningEventBootingNewKernel, "booting into distro kernel")
	return eventEmitter, kernel.RunKexec(info)
}
