package report

import (
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/metal-core/client/device"
	"git.f-i-ts.de/cloud-native/metal/metal-hammer/metal-core/models"

	log "github.com/inconshreveable/log15"
	"github.com/pkg/errors"
)

type Report struct {
	Client          *device.Client
	ConsolePassword string
	DeviceUUID      string
	InstallError    error
}

// ReportInstallation will tell metal-core the result of the installation
func (r *Report) ReportInstallation() error {
	report := &models.DomainReport{
		Success:         true,
		ConsolePassword: &r.ConsolePassword,
	}
	if r.InstallError != nil {
		message := r.InstallError.Error()
		report.Success = false
		report.Message = &message
	}

	params := device.NewReportParams()
	params.SetBody(report)
	params.ID = r.DeviceUUID
	resp, err := r.Client.Report(params)
	if err != nil {
		return errors.Wrap(err, "unable to report image installation")
	}
	log.Info("report image installation was successful", "response", resp.Payload)
	return nil
}
