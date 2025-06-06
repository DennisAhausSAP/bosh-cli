package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	// Should only be imported here to avoid leaking use of goflags through project
	goflags "github.com/jessevdk/go-flags"

	. "github.com/cloudfoundry/bosh-cli/v7/cmd/opts" //nolint:staticcheck
)

type Factory struct {
	deps BasicDeps
}

func NewFactory(deps BasicDeps) Factory {
	return Factory{deps: deps}
}

func (f Factory) New(args []string) (Cmd, error) {
	var cmdOpts interface{}

	boshOpts := &BoshOpts{}

	boshOpts.VersionOpt = func() error {
		return &goflags.Error{
			Type:    goflags.ErrHelp,
			Message: fmt.Sprintf("version %s\n", VersionLabel),
		}
	}

	parser := goflags.NewParser(boshOpts, goflags.HelpFlag|goflags.PassDoubleDash)

	for _, c := range parser.Commands() {
		docsURL := "https://bosh.io/docs/cli-v2#" + c.Name

		c.LongDescription = c.ShortDescription + "\n\n" + docsURL

		fillerLen := 50 - len(c.ShortDescription)
		if fillerLen < 0 {
			fillerLen = 0
		}

		c.ShortDescription += strings.Repeat(" ", fillerLen+1) + docsURL
	}

	parser.CommandHandler = func(command goflags.Commander, extraArgs []string) error {
		if opts, ok := command.(*SSHOpts); ok {
			if len(opts.Command) == 0 {
				opts.Command = extraArgs
				extraArgs = []string{}
			}
		}

		if opts, ok := command.(*AliasEnvOpts); ok {
			opts.URL = boshOpts.EnvironmentOpt
			opts.CACert = boshOpts.CACertOpt
		}

		if opts, ok := command.(*EventsOpts); ok {
			opts.Deployment = boshOpts.DeploymentOpt
		}

		if opts, ok := command.(*VMsOpts); ok {
			opts.Deployment = boshOpts.DeploymentOpt
		}

		if opts, ok := command.(*InstancesOpts); ok {
			opts.Deployment = boshOpts.DeploymentOpt
		}

		if opts, ok := command.(*TasksOpts); ok {
			opts.Deployment = boshOpts.DeploymentOpt
		}

		if opts, ok := command.(*TaskOpts); ok {
			opts.Deployment = boshOpts.DeploymentOpt
		}

		if opts, ok := command.(*CancelTasksOpts); ok {
			opts.Deployment = boshOpts.DeploymentOpt
		}

		if len(extraArgs) > 0 {
			errMsg := "Command '%T' does not support extra arguments: %s"
			return fmt.Errorf(errMsg, command, strings.Join(extraArgs, ", ")) //nolint:staticcheck
		}

		cmdOpts = command

		return nil
	}

	boshOpts.SSH.GatewayFlags.UUIDGen = f.deps.UUIDGen  //nolint:staticcheck
	boshOpts.SCP.GatewayFlags.UUIDGen = f.deps.UUIDGen  //nolint:staticcheck
	boshOpts.Logs.GatewayFlags.UUIDGen = f.deps.UUIDGen //nolint:staticcheck
	boshOpts.Pcap.GatewayFlags.UUIDGen = f.deps.UUIDGen //nolint:staticcheck

	helpText := bytes.NewBufferString("")
	parser.WriteHelp(helpText)

	_, err := parser.ParseArgs(args)

	// --help and --version result in errors; turn them into successful output cmds
	var typedErr *goflags.Error
	if errors.As(err, &typedErr) {
		if errors.Is(typedErr.Type, goflags.ErrHelp) {
			cmdOpts = &MessageOpts{Message: typedErr.Message}
			err = nil
		}
	}

	if _, ok := cmdOpts.(*HelpOpts); ok {
		cmdOpts = &MessageOpts{Message: helpText.String()}
	}

	return NewCmd(*boshOpts, cmdOpts, f.deps), err
}
