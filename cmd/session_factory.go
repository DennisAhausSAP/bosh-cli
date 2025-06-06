package cmd

import (
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"

	cmdconf "github.com/cloudfoundry/bosh-cli/v7/cmd/config"
	. "github.com/cloudfoundry/bosh-cli/v7/cmd/opts" //nolint:staticcheck
	boshui "github.com/cloudfoundry/bosh-cli/v7/ui"
)

func NewSessionFromOpts(
	opts BoshOpts,
	config cmdconf.Config,
	ui boshui.UI,
	printEnvironment bool,
	printDeployment bool,
	fs boshsys.FileSystem,
	logger boshlog.Logger,
) Session {
	context := NewSessionContextImpl(opts, config, fs)

	return NewSessionImpl(context, ui, printEnvironment, printDeployment, logger)
}
