package deployment_test

import (
	"time"

	biagentclient "github.com/cloudfoundry/bosh-agent/v2/agentclient"
	bias "github.com/cloudfoundry/bosh-agent/v2/agentclient/applyspec"
	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"
	fakesys "github.com/cloudfoundry/bosh-utils/system/fakes"
	fakeuuid "github.com/cloudfoundry/bosh-utils/uuid/fakes"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mockagentclient "github.com/cloudfoundry/bosh-cli/v7/agentclient/mocks"
	mockblobstore "github.com/cloudfoundry/bosh-cli/v7/blobstore/mocks"
	bicloud "github.com/cloudfoundry/bosh-cli/v7/cloud"
	mockcloud "github.com/cloudfoundry/bosh-cli/v7/cloud/mocks"
	biconfig "github.com/cloudfoundry/bosh-cli/v7/config"
	. "github.com/cloudfoundry/bosh-cli/v7/deployment"
	bidisk "github.com/cloudfoundry/bosh-cli/v7/deployment/disk"
	biinstance "github.com/cloudfoundry/bosh-cli/v7/deployment/instance"
	mockinstancestate "github.com/cloudfoundry/bosh-cli/v7/deployment/instance/state/mocks"
	bideplmanifest "github.com/cloudfoundry/bosh-cli/v7/deployment/manifest"
	bisshtunnel "github.com/cloudfoundry/bosh-cli/v7/deployment/sshtunnel"
	bivm "github.com/cloudfoundry/bosh-cli/v7/deployment/vm"
	bistemcell "github.com/cloudfoundry/bosh-cli/v7/stemcell"
	fakebiui "github.com/cloudfoundry/bosh-cli/v7/ui/fakes"
)

var _ = Describe("Deployment", func() {

	var (
		mockCtrl *gomock.Controller
		logger   boshlog.Logger
		fs       boshsys.FileSystem

		fakeUUIDGenerator      *fakeuuid.FakeGenerator
		fakeRepoUUIDGenerator  *fakeuuid.FakeGenerator
		deploymentStateService biconfig.DeploymentStateService
		vmRepo                 biconfig.VMRepo
		diskRepo               biconfig.DiskRepo
		stemcellRepo           biconfig.StemcellRepo

		mockCloud       *mockcloud.MockCloud
		mockAgentClient *mockagentclient.MockAgentClient

		mockStateBuilderFactory *mockinstancestate.MockBuilderFactory
		mockStateBuilder        *mockinstancestate.MockBuilder
		mockState               *mockinstancestate.MockState

		mockBlobstore *mockblobstore.MockBlobstore

		fakeStage *fakebiui.FakeStage

		deploymentFactory Factory

		stemcellApiVersion = 2
		deployment         Deployment
		skipDrain          bool
	)

	var allowApplySpecToBeCreated = func() {
		jobName := "fake-job-name"
		jobIndex := 0

		applySpec := bias.ApplySpec{
			Deployment: "test-release",
			Index:      jobIndex,
			Packages:   map[string]bias.Blob{},
			Networks: map[string]interface{}{
				"network-1": map[string]interface{}{
					"cloud_properties": map[string]interface{}{},
					"type":             "dynamic",
					"ip":               "",
				},
			},
			Job: bias.Job{
				Name:      jobName,
				Templates: []bias.Blob{},
			},
			RenderedTemplatesArchive: bias.RenderedTemplatesArchiveSpec{},
			ConfigurationHash:        "",
		}

		mockStateBuilderFactory.EXPECT().NewBuilder(mockBlobstore, mockAgentClient).Return(mockStateBuilder).AnyTimes()
		mockState.EXPECT().ToApplySpec().Return(applySpec).AnyTimes()
	}

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		logger = boshlog.NewLogger(boshlog.LevelNone)
		fs = fakesys.NewFakeFileSystem()

		fakeUUIDGenerator = fakeuuid.NewFakeGenerator()
		deploymentStateService = biconfig.NewFileSystemDeploymentStateService(fs, fakeUUIDGenerator, logger, "/deployment.json")

		fakeRepoUUIDGenerator = fakeuuid.NewFakeGenerator()
		vmRepo = biconfig.NewVMRepo(deploymentStateService)
		diskRepo = biconfig.NewDiskRepo(deploymentStateService, fakeRepoUUIDGenerator)
		stemcellRepo = biconfig.NewStemcellRepo(deploymentStateService, fakeRepoUUIDGenerator)

		mockCloud = mockcloud.NewMockCloud(mockCtrl)
		mockAgentClient = mockagentclient.NewMockAgentClient(mockCtrl)

		fakeStage = fakebiui.NewFakeStage()

		pingTimeout := 10 * time.Second
		pingDelay := 500 * time.Millisecond
		deploymentFactory = NewFactory(pingTimeout, pingDelay)

		skipDrain = false
	})

	JustBeforeEach(func() {
		// all these local factories & managers are just used to construct a Deployment based on the deployment state
		diskManagerFactory := bidisk.NewManagerFactory(diskRepo, logger)
		diskDeployer := bivm.NewDiskDeployer(diskManagerFactory, diskRepo, logger, false)

		vmManagerFactory := bivm.NewManagerFactory(vmRepo, stemcellRepo, diskDeployer, fakeUUIDGenerator, fs, logger)
		sshTunnelFactory := bisshtunnel.NewFactory(logger)

		mockStateBuilderFactory = mockinstancestate.NewMockBuilderFactory(mockCtrl)
		mockStateBuilder = mockinstancestate.NewMockBuilder(mockCtrl)
		mockState = mockinstancestate.NewMockState(mockCtrl)

		instanceFactory := biinstance.NewFactory(mockStateBuilderFactory)
		instanceManagerFactory := biinstance.NewManagerFactory(sshTunnelFactory, instanceFactory, logger)
		stemcellManagerFactory := bistemcell.NewManagerFactory(stemcellRepo)

		mockBlobstore = mockblobstore.NewMockBlobstore(mockCtrl)

		deploymentManagerFactory := NewManagerFactory(vmManagerFactory, instanceManagerFactory, diskManagerFactory, stemcellManagerFactory, deploymentFactory)
		deploymentManager := deploymentManagerFactory.NewManager(mockCloud, mockAgentClient, mockBlobstore)

		allowApplySpecToBeCreated()

		var err error
		deployment, _, err = deploymentManager.FindCurrent()
		Expect(err).ToNot(HaveOccurred())
		// Note: deployment will be nil if the config has no vms, disks, or stemcells
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Describe("Delete", func() {

		var expectNormalFlow = func() {
			gomock.InOrder(
				mockCloud.EXPECT().HasVM("fake-vm-cid").Return(true, nil),
				mockAgentClient.EXPECT().Ping().Return("any-state", nil), // ping to make sure agent is responsive
				mockAgentClient.EXPECT().RunScript("pre-stop", map[string]interface{}{}),
				mockAgentClient.EXPECT().Drain("shutdown"), // drain all jobs
				mockAgentClient.EXPECT().Stop(),            // stop all jobs
				mockAgentClient.EXPECT().RunScript("post-stop", map[string]interface{}{}),
				mockAgentClient.EXPECT().ListDisk().Return([]string{"fake-disk-cid"}, nil), // get mounted disks to be unmounted
				mockAgentClient.EXPECT().UnmountDisk("fake-disk-cid"),
				mockCloud.EXPECT().DeleteVM("fake-vm-cid"),
				mockCloud.EXPECT().DeleteDisk("fake-disk-cid"),
				mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid"),
			)
		}

		var expectDrainlessFlow = func() {
			gomock.InOrder(
				mockCloud.EXPECT().HasVM("fake-vm-cid").Return(true, nil),
				mockAgentClient.EXPECT().Ping().Return("any-state", nil), // ping to make sure agent is responsive
				mockAgentClient.EXPECT().RunScript("pre-stop", map[string]interface{}{}),
				mockAgentClient.EXPECT().Stop(), // stop all jobs
				mockAgentClient.EXPECT().RunScript("post-stop", map[string]interface{}{}),
				mockAgentClient.EXPECT().ListDisk().Return([]string{"fake-disk-cid"}, nil), // get mounted disks to be unmounted
				mockAgentClient.EXPECT().UnmountDisk("fake-disk-cid"),
				mockCloud.EXPECT().DeleteVM("fake-vm-cid"),
				mockCloud.EXPECT().DeleteDisk("fake-disk-cid"),
				mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid"),
			)
		}

		Context("when the deployment has been deployed", func() {
			BeforeEach(func() {
				// create deployment manifest yaml file
				err := deploymentStateService.Save(biconfig.DeploymentState{
					DirectorID:        "fake-director-id",
					InstallationID:    "fake-installation-id",
					CurrentVMCID:      "fake-vm-cid",
					CurrentStemcellID: "fake-stemcell-guid",
					CurrentDiskID:     "fake-disk-guid",
					Disks: []biconfig.DiskRecord{
						{
							ID:   "fake-disk-guid",
							CID:  "fake-disk-cid",
							Size: 100,
						},
					},
					Stemcells: []biconfig.StemcellRecord{
						{
							ID:  "fake-stemcell-guid",
							CID: "fake-stemcell-cid",
						},
					},
				})
				Expect(err).ToNot(HaveOccurred())
			})

			It("stops agent, unmounts disk, deletes vm, deletes disk, deletes stemcell", func() {
				expectNormalFlow()

				err := deployment.Delete(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())
			})

			It("skips draining if specified", func() {
				skipDrain = true
				expectDrainlessFlow()

				err := deployment.Delete(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())
			})

			It("logs validation stages", func() {
				expectNormalFlow()

				err := deployment.Delete(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeStage.PerformCalls).To(Equal([]*fakebiui.PerformCall{
					{Name: "Waiting for the agent on VM 'fake-vm-cid'"},
					{Name: "Running the pre-stop scripts 'unknown/0'"},
					{Name: "Draining jobs on instance 'unknown/0'"},
					{Name: "Stopping jobs on instance 'unknown/0'"},
					{Name: "Running the post-stop scripts 'unknown/0'"},
					{Name: "Unmounting disk 'fake-disk-cid'"},
					{Name: "Deleting VM 'fake-vm-cid'"},
					{Name: "Deleting disk 'fake-disk-cid'"},
					{Name: "Deleting stemcell 'fake-stemcell-cid'"},
				}))
			})

			It("clears current vm, disk and stemcell", func() {
				expectNormalFlow()

				err := deployment.Delete(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())

				_, found, err := vmRepo.FindCurrent()
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeFalse(), "should be no current VM")

				_, found, err = diskRepo.FindCurrent()
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeFalse(), "should be no current disk")

				diskRecords, err := diskRepo.All()
				Expect(err).ToNot(HaveOccurred())
				Expect(diskRecords).To(BeEmpty(), "expected no disk records")

				_, found, err = stemcellRepo.FindCurrent()
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeFalse(), "should be no current stemcell")

				stemcellRecords, err := stemcellRepo.All()
				Expect(err).ToNot(HaveOccurred())
				Expect(stemcellRecords).To(BeEmpty(), "expected no stemcell records")
			})

			// TODO: It'd be nice to test recovering after agent was responsive, before timeout (hard to do with gomock)
			Context("when agent is unresponsive", func() {
				BeforeEach(func() {
					// reduce timout & delay to reduce test duration
					pingTimeout := 1 * time.Second
					pingDelay := 100 * time.Millisecond
					deploymentFactory = NewFactory(pingTimeout, pingDelay)
				})

				It("times out pinging agent, deletes vm, deletes disk, deletes stemcell", func() {
					gomock.InOrder(
						mockCloud.EXPECT().HasVM("fake-vm-cid").Return(true, nil),
						mockAgentClient.EXPECT().Ping().Return("", bosherr.Error("unresponsive agent")).AnyTimes(), // ping to make sure agent is responsive
						mockCloud.EXPECT().DeleteVM("fake-vm-cid"),
						mockCloud.EXPECT().DeleteDisk("fake-disk-cid"),
						mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid"),
					)

					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("and delete previously suceeded", func() {
				JustBeforeEach(func() {
					expectNormalFlow()

					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())

					// reset event log recording
					fakeStage = fakebiui.NewFakeStage()
				})

				It("does not delete anything", func() {
					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeStage.PerformCalls).To(BeEmpty())
				})
			})
		})

		Context("when nothing has been deployed", func() {
			BeforeEach(func() {
				err := deploymentStateService.Save(biconfig.DeploymentState{})
				Expect(err).ToNot(HaveOccurred())
			})

			JustBeforeEach(func() {
				// A previous JustBeforeEach uses FindCurrent to define deployment,
				// which would return a nil if the config is empty.
				// So we have to make a fake empty deployment to test it.
				deployment = deploymentFactory.NewDeployment([]biinstance.Instance{}, []bidisk.Disk{}, []bistemcell.CloudStemcell{})
			})

			It("does not delete anything", func() {
				err := deployment.Delete(skipDrain, fakeStage)
				Expect(err).NotTo(HaveOccurred())

				Expect(fakeStage.PerformCalls).To(BeEmpty())
			})
		})

		Context("when VM has been deployed", func() {
			var (
				expectHasVM *gomock.Call
			)
			BeforeEach(func() {
				err := deploymentStateService.Save(biconfig.DeploymentState{})
				Expect(err).ToNot(HaveOccurred())
				err = vmRepo.UpdateCurrent("fake-vm-cid")
				Expect(err).ToNot(HaveOccurred())

				expectHasVM = mockCloud.EXPECT().HasVM("fake-vm-cid").Return(true, nil)
			})

			It("stops the agent and deletes the VM", func() {
				gomock.InOrder(
					mockAgentClient.EXPECT().Ping().Return("any-state", nil), // ping to make sure agent is responsive
					mockAgentClient.EXPECT().RunScript("pre-stop", map[string]interface{}{}),
					mockAgentClient.EXPECT().Drain("shutdown"), // drain all jobs
					mockAgentClient.EXPECT().Stop(),            // stop all jobs
					mockAgentClient.EXPECT().RunScript("post-stop", map[string]interface{}{}),
					mockAgentClient.EXPECT().ListDisk().Return([]string{"fake-disk-cid"}, nil), // get mounted disks to be unmounted
					mockAgentClient.EXPECT().UnmountDisk("fake-disk-cid"),
					mockCloud.EXPECT().DeleteVM("fake-vm-cid"),
				)

				err := deployment.Delete(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())
			})

			Context("when VM has been deleted manually (outside of bosh)", func() {
				BeforeEach(func() {
					expectHasVM.Return(false, nil)
				})

				It("skips agent shutdown & deletes the VM (to ensure related resources are released by the CPI)", func() {
					mockCloud.EXPECT().DeleteVM("fake-vm-cid")

					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())
				})

				It("ignores VMNotFound errors", func() {
					mockCloud.EXPECT().DeleteVM("fake-vm-cid").Return(bicloud.NewCPIError("delete_vm", bicloud.CmdError{
						Type:    bicloud.VMNotFoundError,
						Message: "fake-vm-not-found-message",
					}))

					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})

		Context("when a current disk exists", func() {
			BeforeEach(func() {
				err := deploymentStateService.Save(biconfig.DeploymentState{})
				Expect(err).ToNot(HaveOccurred())
				diskRecord, err := diskRepo.Save("fake-disk-cid", 100, nil)
				Expect(err).ToNot(HaveOccurred())
				err = diskRepo.UpdateCurrent(diskRecord.ID)
				Expect(err).ToNot(HaveOccurred())
			})

			It("deletes the disk", func() {
				mockCloud.EXPECT().DeleteDisk("fake-disk-cid")

				err := deployment.Delete(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())
			})

			Context("when current disk has been deleted manually (outside of bosh)", func() {
				It("deletes the disk (to ensure related resources are released by the CPI)", func() {
					mockCloud.EXPECT().DeleteDisk("fake-disk-cid")

					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())
				})

				It("ignores DiskNotFound errors", func() {
					mockCloud.EXPECT().DeleteDisk("fake-disk-cid").Return(bicloud.NewCPIError("delete_disk", bicloud.CmdError{
						Type:    bicloud.DiskNotFoundError,
						Message: "fake-disk-not-found-message",
					}))

					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})

		Context("when a current stemcell exists", func() {
			BeforeEach(func() {
				err := deploymentStateService.Save(biconfig.DeploymentState{})
				Expect(err).ToNot(HaveOccurred())
				stemcellRecord, err := stemcellRepo.Save("fake-stemcell-name", "fake-stemcell-version", "fake-stemcell-cid", stemcellApiVersion)
				Expect(err).ToNot(HaveOccurred())
				err = stemcellRepo.UpdateCurrent(stemcellRecord.ID)
				Expect(err).ToNot(HaveOccurred())
			})

			It("deletes the stemcell", func() {
				mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid")

				err := deployment.Delete(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())
			})

			Context("when current stemcell has been deleted manually (outside of bosh)", func() {
				It("deletes the stemcell (to ensure related resources are released by the CPI)", func() {
					mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid")

					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())
				})

				It("ignores StemcellNotFound errors", func() {
					mockCloud.EXPECT().DeleteStemcell("fake-stemcell-cid").Return(bicloud.NewCPIError("delete_stemcell", bicloud.CmdError{
						Type:    bicloud.StemcellNotFoundError,
						Message: "fake-stemcell-not-found-message",
					}))

					err := deployment.Delete(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})
	})

	Describe("Stop", func() {

		var expectNormalFlow = func() {
			gomock.InOrder(
				mockAgentClient.EXPECT().Ping().Return("any-state", nil),
				mockAgentClient.EXPECT().RunScript("pre-stop", map[string]interface{}{}),
				mockAgentClient.EXPECT().Drain("shutdown"),
				mockAgentClient.EXPECT().Stop(),
				mockAgentClient.EXPECT().RunScript("post-stop", map[string]interface{}{}),
			)
		}

		var expectDrainlessFlow = func() {
			gomock.InOrder(
				mockAgentClient.EXPECT().Ping().Return("any-state", nil),
				mockAgentClient.EXPECT().RunScript("pre-stop", map[string]interface{}{}),
				mockAgentClient.EXPECT().Stop(),
				mockAgentClient.EXPECT().RunScript("post-stop", map[string]interface{}{}),
			)
		}

		Context("when the deployment has been deployed", func() {
			BeforeEach(func() {
				// create deployment manifest yaml file
				err := deploymentStateService.Save(biconfig.DeploymentState{
					DirectorID:        "fake-director-id",
					InstallationID:    "fake-installation-id",
					CurrentVMCID:      "fake-vm-cid",
					CurrentStemcellID: "fake-stemcell-guid",
					CurrentDiskID:     "fake-disk-guid",
					Disks: []biconfig.DiskRecord{
						{
							ID:   "fake-disk-guid",
							CID:  "fake-disk-cid",
							Size: 100,
						},
					},
					Stemcells: []biconfig.StemcellRecord{
						{
							ID:  "fake-stemcell-guid",
							CID: "fake-stemcell-cid",
						},
					},
				})
				Expect(err).ToNot(HaveOccurred())
			})

			It("stops agent and executes the pre-stop and post-stop scripts", func() {
				expectNormalFlow()

				err := deployment.Stop(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())
			})

			It("skips draining if specified", func() {
				skipDrain = true
				expectDrainlessFlow()

				err := deployment.Stop(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())
			})

			It("logs validation stages", func() {
				expectNormalFlow()

				err := deployment.Stop(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeStage.PerformCalls).To(Equal([]*fakebiui.PerformCall{
					{Name: "Waiting for the agent on VM 'fake-vm-cid'"},
					{Name: "Running the pre-stop scripts 'unknown/0'"},
					{Name: "Draining jobs on instance 'unknown/0'"},
					{Name: "Stopping jobs on instance 'unknown/0'"},
					{Name: "Running the post-stop scripts 'unknown/0'"},
				}))
			})

			Context("when agent is unresponsive", func() {
				BeforeEach(func() {
					// reduce timout & delay to reduce test duration
					pingTimeout := 1 * time.Second
					pingDelay := 100 * time.Millisecond
					deploymentFactory = NewFactory(pingTimeout, pingDelay)
				})

				It("times out pinging agent and does nothing", func() {
					gomock.InOrder(
						mockAgentClient.EXPECT().Ping().Return("", bosherr.Error("unresponsive agent")).AnyTimes(),
					)

					err := deployment.Stop(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("and delete previously suceeded", func() {
				JustBeforeEach(func() {
					expectNormalFlow()

					err := deployment.Stop(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())

					// reset event log recording
					fakeStage = fakebiui.NewFakeStage()
				})

				It("does not delete anything", func() {
					err := deployment.Stop(skipDrain, fakeStage)
					Expect(err).ToNot(HaveOccurred())

					Expect(fakeStage.PerformCalls).To(BeEmpty())
				})
			})
		})

		Context("when nothing has been deployed", func() {
			BeforeEach(func() {
				err := deploymentStateService.Save(biconfig.DeploymentState{})
				Expect(err).ToNot(HaveOccurred())
			})

			JustBeforeEach(func() {
				// A previous JustBeforeEach uses FindCurrent to define deployment,
				// which would return a nil if the config is empty.
				// So we have to make a fake empty deployment to test it.
				deployment = deploymentFactory.NewDeployment([]biinstance.Instance{}, []bidisk.Disk{}, []bistemcell.CloudStemcell{})
			})

			It("does not stop anything", func() {
				err := deployment.Stop(skipDrain, fakeStage)
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeStage.PerformCalls).To(BeEmpty())
			})
		})
	})

	Describe("Start", func() {
		var agentRunningState = biagentclient.AgentState{JobState: "running"}
		var expectNormalFlow = func() {
			gomock.InOrder(
				mockAgentClient.EXPECT().Ping().Return("any-state", nil), // ping to make sure agent is responsive
				mockAgentClient.EXPECT().RunScript("pre-start", map[string]interface{}{}),
				mockAgentClient.EXPECT().Start(), // stop all jobs
				mockAgentClient.EXPECT().GetState().AnyTimes().Return(agentRunningState, nil),
				mockAgentClient.EXPECT().RunScript("post-start", map[string]interface{}{}),
			)
		}

		var update = bideplmanifest.Update{
			UpdateWatchTime: bideplmanifest.WatchTime{
				Start: 0,
				End:   5478,
			},
		}

		Context("when the deployment has been deployed", func() {
			BeforeEach(func() {
				// create deployment manifest yaml file
				err := deploymentStateService.Save(biconfig.DeploymentState{
					DirectorID:        "fake-director-id",
					InstallationID:    "fake-installation-id",
					CurrentVMCID:      "fake-vm-cid",
					CurrentStemcellID: "fake-stemcell-guid",
					CurrentDiskID:     "fake-disk-guid",
					Disks: []biconfig.DiskRecord{
						{
							ID:   "fake-disk-guid",
							CID:  "fake-disk-cid",
							Size: 100,
						},
					},
					Stemcells: []biconfig.StemcellRecord{
						{
							ID:  "fake-stemcell-guid",
							CID: "fake-stemcell-cid",
						},
					},
				})
				Expect(err).ToNot(HaveOccurred())
			})

			It("starts agent and executes the pre-start and post-start scripts", func() {
				expectNormalFlow()

				err := deployment.Start(fakeStage, update)
				Expect(err).ToNot(HaveOccurred())
			})

			It("logs validation stages", func() {
				expectNormalFlow()

				err := deployment.Start(fakeStage, update)
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeStage.PerformCalls).To(Equal([]*fakebiui.PerformCall{
					{Name: "Waiting for the agent on VM 'fake-vm-cid'"},
					{Name: "Running the pre-start scripts 'unknown/0'"},
					{Name: "Starting the agent 'unknown/0'"},
					{Name: "Waiting for instance 'unknown/0' to be running"},
					{Name: "Running the post-start scripts 'unknown/0'"},
				}))
			})

			Context("when agent is unresponsive", func() {
				BeforeEach(func() {
					// reduce timout & delay to reduce test duration
					pingTimeout := 1 * time.Second
					pingDelay := 100 * time.Millisecond
					deploymentFactory = NewFactory(pingTimeout, pingDelay)
				})

				It("times out pinging agent and does nothing", func() {
					gomock.InOrder(
						mockAgentClient.EXPECT().Ping().Return("", bosherr.Error("unresponsive agent")).AnyTimes(), // ping to make sure agent is responsive
					)

					err := deployment.Start(fakeStage, update)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("and start previously suceeded", func() {
				var expectNormalFlow = func() {
					gomock.InOrder(
						mockAgentClient.EXPECT().Ping().Return("any-state", nil).AnyTimes(),
						mockAgentClient.EXPECT().RunScript("pre-start", map[string]interface{}{}).AnyTimes(),
						mockAgentClient.EXPECT().Start().AnyTimes(),
						mockAgentClient.EXPECT().GetState().AnyTimes().Return(agentRunningState, nil),
						mockAgentClient.EXPECT().RunScript("post-start", map[string]interface{}{}).AnyTimes(),
					)
				}

				JustBeforeEach(func() {
					expectNormalFlow()

					err := deployment.Start(fakeStage, update)
					Expect(err).ToNot(HaveOccurred())

					// reset event log recording
					fakeStage = fakebiui.NewFakeStage()
				})

				It("does execute the normal flow", func() {
					expectNormalFlow()

					err := deployment.Start(fakeStage, update)
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})

		Context("when nothing has been deployed", func() {
			BeforeEach(func() {
				err := deploymentStateService.Save(biconfig.DeploymentState{})
				Expect(err).ToNot(HaveOccurred())
			})

			JustBeforeEach(func() {
				// A previous JustBeforeEach uses FindCurrent to define deployment,
				// which would return a nil if the config is empty.
				// So we have to make a fake empty deployment to test it.
				deployment = deploymentFactory.NewDeployment([]biinstance.Instance{}, []bidisk.Disk{}, []bistemcell.CloudStemcell{})
			})

			It("does not start anything", func() {
				err := deployment.Start(fakeStage, update)
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeStage.PerformCalls).To(BeEmpty())
			})
		})
	})
})
