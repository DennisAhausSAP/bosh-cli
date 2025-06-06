package cmd

import (
	bosherr "github.com/cloudfoundry/bosh-utils/errors"

	. "github.com/cloudfoundry/bosh-cli/v7/cmd/opts" //nolint:staticcheck
	boshdir "github.com/cloudfoundry/bosh-cli/v7/director"
	boshtpl "github.com/cloudfoundry/bosh-cli/v7/director/template"
	boshui "github.com/cloudfoundry/bosh-cli/v7/ui"
)

type UpdateRuntimeConfigCmd struct {
	ui              boshui.UI
	director        boshdir.Director
	releaseUploader ReleaseUploader
}

func NewUpdateRuntimeConfigCmd(ui boshui.UI, director boshdir.Director, releaseUploader ReleaseUploader) UpdateRuntimeConfigCmd {
	return UpdateRuntimeConfigCmd{ui: ui, director: director, releaseUploader: releaseUploader}
}

func (c UpdateRuntimeConfigCmd) Run(opts UpdateRuntimeConfigOpts) error {
	tpl := boshtpl.NewTemplate(opts.Args.RuntimeConfig.Bytes)

	bytes, err := tpl.Evaluate(opts.VarFlags.AsVariables(), opts.OpsFlags.AsOp(), boshtpl.EvaluateOpts{}) //nolint:staticcheck
	if err != nil {
		return bosherr.WrapErrorf(err, "Evaluating runtime config")
	}

	configDiff, err := c.director.DiffRuntimeConfig(opts.Name, bytes, opts.NoRedact)
	if err != nil {
		return err
	}

	diff := NewDiff(configDiff.Diff)
	diff.Print(c.ui)

	if opts.FixReleases {
		bytes, err = c.releaseUploader.UploadReleasesWithFix(bytes)
	} else {
		bytes, err = c.releaseUploader.UploadReleases(bytes)
	}

	if err != nil {
		return err
	}

	err = c.ui.AskForConfirmation()
	if err != nil {
		return err
	}

	return c.director.UpdateRuntimeConfig(opts.Name, bytes)
}
