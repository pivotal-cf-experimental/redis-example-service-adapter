package adapter_test

import (
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/pivotal-cf-experimental/redis-example-service-adapter/adapter"
	"github.com/pivotal-cf/on-demand-services-sdk/bosh"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"

	"io/ioutil"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"gopkg.in/yaml.v2"
)

var _ = Describe("Redis Service Adapter", func() {

	const ProvidedRedisServerInstanceGroupName = "redis-server"

	adapter.CurrentPasswordGenerator = func() (string, error) {
		return "really random password", nil
	}

	var (
		defaultServiceReleases   serviceadapter.ServiceReleases
		defaultRequestParameters map[string]interface{}
		manifestGenerator        adapter.ManifestGenerator
		dedicatedPlan            serviceadapter.Plan
		highMemoryPlan           serviceadapter.Plan
		stderr                   *gbytes.Buffer
		stderrLogger             *log.Logger
		config                   adapter.Config
	)

	BeforeEach(func() {
		dedicatedPlan = serviceadapter.Plan{
			Properties: map[string]interface{}{
				"persistence": true,
			},
			InstanceGroups: []serviceadapter.InstanceGroup{
				{
					Name:               "redis-server",
					VMType:             "dedicated-vm",
					VMExtensions:       []string{"dedicated-extensions"},
					PersistentDiskType: "dedicated-disk",
					Networks:           []string{"dedicated-network"},
					Instances:          45,
					AZs:                []string{"dedicated-az1", "dedicated-az2"},
				},
				{
					Name:         "health-check",
					VMType:       "health-check-vm",
					Lifecycle:    adapter.LifecycleErrandType,
					VMExtensions: []string{"health-check-extensions"},
					Networks:     []string{"health-check-network"},
					Instances:    1,
					AZs:          []string{"health-check-az1"},
				},
				{
					Name:         "cleanup-data",
					VMType:       "cleanup-data-vm",
					Lifecycle:    adapter.LifecycleErrandType,
					VMExtensions: []string{"cleanup-data-extensions"},
					Networks:     []string{"cleanup-data-network"},
					Instances:    1,
					AZs:          []string{"cleanup-data-az1"},
				},
			},
			Update: &serviceadapter.Update{
				Canaries:        1,
				CanaryWatchTime: "100-200",
				UpdateWatchTime: "100-200",
				MaxInFlight:     5,
				Serial:          nil,
			},
		}

		highMemoryPlan = serviceadapter.Plan{
			Properties: map[string]interface{}{
				"persistence": false,
			},
			InstanceGroups: []serviceadapter.InstanceGroup{
				{
					Name:               "redis-server",
					VMType:             "high-memory-vm",
					PersistentDiskType: "high-memory-disk",
					Networks:           []string{"high-memory-network"},
					Instances:          42,
					AZs:                []string{"high-memory-az1", "high-memory-az2"},
				},
			},
		}

		defaultRequestParameters = map[string]interface{}{}

		defaultServiceReleases = serviceadapter.ServiceReleases{
			{
				Name:    "some-release-name",
				Version: "4",
				Jobs: []string{
					adapter.RedisJobName,
					adapter.HealthCheckErrandName,
					adapter.CleanupDataErrandName,
				},
			},
		}

		stderr = gbytes.NewBuffer()
		stderrLogger = log.New(io.MultiWriter(stderr, GinkgoWriter), "", log.LstdFlags)

		config.RedisInstanceGroupName = "redis-server"
		manifestGenerator = adapter.ManifestGenerator{Config: config, StderrLogger: stderrLogger}

	})

	Describe("Generating manifests", func() {
		It("sets the instance group's redis persistence property to be 'no' when using high memory plan", func() {
			oldManifest := createDefaultOldManifest()

			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				highMemoryPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(
				generated.Manifest.
					InstanceGroups[0].
					Properties["redis"].(map[interface{}]interface{})["persistence"],
			).To(Equal("no"))
		})

		Context("validation of 'context' property", func() {

			DescribeTable("logging validation of context and platform when requestParams is",
				func(params map[string]interface{}) {
					oldManifest := createDefaultOldManifest()
					_, generateErr := generateManifest(
						manifestGenerator,
						defaultServiceReleases,
						highMemoryPlan,
						params,
						&oldManifest,
						nil,
						nil,
					)
					Expect(generateErr).NotTo(HaveOccurred())
					Expect(stderr).To(gbytes.Say(`Non Cloud Foundry platform \(or pre OSBAPI 2\.13\) detected`))
				},
				Entry("an empty map", map[string]interface{}{}),
				Entry("a map with empty context", map[string]interface{}{"context": map[string]interface{}{}}),
				Entry("a map with a context but empty platform", map[string]interface{}{
					"context": map[string]interface{}{
						"platform": "",
					},
				}),
				Entry("a map with a context but non-cloudfoundry platform", map[string]interface{}{
					"context": map[string]interface{}{
						"platform": "not-cloudfoundry",
					},
				}),
			)

			It("does not log when the platform is cloudfoundry", func() {
				params := map[string]interface{}{
					"context": map[string]interface{}{
						"platform": "cloudfoundry",
					},
				}

				oldManifest := createDefaultOldManifest()
				_, generateErr := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					highMemoryPlan,
					params,
					&oldManifest,
					nil,
					nil,
				)
				Expect(generateErr).NotTo(HaveOccurred())
				Expect(stderr).NotTo(gbytes.Say(`Non Cloud Foundry platform \(or pre OSBAPI 2\.13\) detected`))
			})

		})

		It("contains only multiple instance groups when different errands have distinct colocation settings", func() {
			oldManifest := createDefaultOldManifest()

			plan := serviceadapter.Plan{
				LifecycleErrands: serviceadapter.LifecycleErrands{
					PreDelete: []serviceadapter.Errand{
						{
							Name:      "health-check",
							Instances: []string{"redis-server"},
						},
						{
							Name: "cleanup-data",
						},
					},
				},
				Properties: map[string]interface{}{
					"persistence":      true,
					"colocated_errand": true,
				},
				InstanceGroups: []serviceadapter.InstanceGroup{
					{
						Name:               "redis-server",
						VMType:             "dedicated-vm",
						VMExtensions:       []string{"dedicated-extensions"},
						PersistentDiskType: "dedicated-disk",
						Networks:           []string{"dedicated-network"},
						Instances:          45,
						AZs:                []string{"dedicated-az1", "dedicated-az2"},
					},
					{
						Name:               "cleanup-data",
						VMType:             "dedicated-vm",
						VMExtensions:       []string{"dedicated-extensions"},
						PersistentDiskType: "dedicated-disk",
						Networks:           []string{"dedicated-network"},
						Instances:          45,
						AZs:                []string{"dedicated-az1", "dedicated-az2"},
						Lifecycle:          "errand",
					},
				},
			}

			colocatedPostDeployPlan := plan
			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				colocatedPostDeployPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(containsJobName(generated.Manifest.InstanceGroups[0].Jobs, "redis-server")).To(BeTrue())
			Expect(containsJobName(generated.Manifest.InstanceGroups[0].Jobs, "health-check")).To(BeTrue())
			Expect(containsJobName(generated.Manifest.InstanceGroups[1].Jobs, "cleanup-data")).To(BeTrue())
			Expect(generated.Manifest.InstanceGroups[1].Lifecycle).To(Equal("errand"))
			Expect(generated.Manifest.InstanceGroups[0].Jobs).To(HaveLen(2))
			Expect(generated.Manifest.InstanceGroups[1].Jobs).To(HaveLen(1))
		})

		It("contains only one instance group and multiple jobs, when `colocated_errand` property is set to true and post_deploy has been configured", func() {
			oldManifest := createDefaultOldManifest()

			plan := serviceadapter.Plan{
				LifecycleErrands: serviceadapter.LifecycleErrands{
					PostDeploy: []serviceadapter.Errand{{
						Name:      "health-check",
						Instances: []string{"redis-server"},
					}},
				},
				Properties: map[string]interface{}{
					"persistence":      true,
					"colocated_errand": true,
				},
				InstanceGroups: []serviceadapter.InstanceGroup{
					{
						Name:               "redis-server",
						VMType:             "dedicated-vm",
						VMExtensions:       []string{"dedicated-extensions"},
						PersistentDiskType: "dedicated-disk",
						Networks:           []string{"dedicated-network"},
						Instances:          45,
						AZs:                []string{"dedicated-az1", "dedicated-az2"},
					},
				},
			}

			colocatedPostDeployPlan := plan
			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				colocatedPostDeployPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(containsJobName(generated.Manifest.InstanceGroups[0].Jobs, "redis-server")).To(BeTrue())
			Expect(containsJobName(generated.Manifest.InstanceGroups[0].Jobs, "health-check")).To(BeTrue())
			Expect(generated.Manifest.InstanceGroups[0].Jobs).To(HaveLen(2))
		})

		It("contains only one instance group and multiple jobs, when `colocated_errand` property is set to true and pre_delete has been configured", func() {
			oldManifest := createDefaultOldManifest()

			plan := serviceadapter.Plan{
				LifecycleErrands: serviceadapter.LifecycleErrands{
					PreDelete: []serviceadapter.Errand{
						{
							Name:      "cleanup-data",
							Instances: []string{"redis-server"},
						},
						{
							Name:      "another-errand",
							Instances: []string{"redis-server"},
						},
					},
				},
				Properties: map[string]interface{}{
					"persistence":      true,
					"colocated_errand": true,
				},
				InstanceGroups: []serviceadapter.InstanceGroup{
					{
						Name:               "redis-server",
						VMType:             "dedicated-vm",
						VMExtensions:       []string{"dedicated-extensions"},
						PersistentDiskType: "dedicated-disk",
						Networks:           []string{"dedicated-network"},
						Instances:          45,
						AZs:                []string{"dedicated-az1", "dedicated-az2"},
					},
				},
			}

			colocatedPreDeletePlan := plan
			defaultServiceReleases[0].Jobs = append(defaultServiceReleases[0].Jobs, "another-errand")
			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				colocatedPreDeletePlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(containsJobName(generated.Manifest.InstanceGroups[0].Jobs, "redis-server")).To(BeTrue())
			Expect(containsJobName(generated.Manifest.InstanceGroups[0].Jobs, "cleanup-data")).To(BeTrue())
			Expect(containsJobName(generated.Manifest.InstanceGroups[0].Jobs, "another-errand")).To(BeTrue())
			Expect(generated.Manifest.InstanceGroups[0].Jobs).To(HaveLen(3))
		})

		It("includes use_short_dns_addresses in bosh features block when property set in plan", func() {
			dedicatedPlan.Properties["use_short_dns_addresses"] = true
			oldManifest := createDefaultOldManifest()

			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(generated.Manifest.Features.UseShortDNSAddresses).ToNot(BeNil())
			Expect(*generated.Manifest.Features.UseShortDNSAddresses).To(BeTrue())
		})

		It("includes use_short_dns_addresses in bosh features block when property set to false in plan", func() {
			dedicatedPlan.Properties["use_short_dns_addresses"] = false
			oldManifest := createDefaultOldManifest()

			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(generated.Manifest.Features.UseShortDNSAddresses).ToNot(BeNil())
			Expect(*generated.Manifest.Features.UseShortDNSAddresses).To(BeFalse())
		})

		It("does not include use_short_dns_addresses in bosh features block when property is not set in plan", func() {
			oldManifest := createDefaultOldManifest()

			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(generated.Manifest.Features.UseShortDNSAddresses).To(BeNil())
		})

		It("always provides a shared link with redis server job", func() {
			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				nil,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			redisServerJob := generated.Manifest.InstanceGroups[0].Jobs[0]
			Expect(redisServerJob.Name).To(Equal("redis-server"))
			Expect(redisServerJob.Provides["redis"].Shared).To(BeTrue())
		})

		It("includes arbitrary feature in bosh features block when property set in plan", func() {
			dedicatedPlan.Properties["something_completely_different"] = "and_now"
			oldManifest := createDefaultOldManifest()

			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(generated.Manifest.Features.ExtraFeatures).To(Equal(map[string]interface{}{
				"something_completely_different": "and_now",
			}))
		})

		Context("setting systest properties on health check", func() {
			var plan serviceadapter.Plan

			BeforeEach(func() {
				plan = serviceadapter.Plan{
					Properties: map[string]interface{}{
						"persistence": false,
					},
					InstanceGroups: []serviceadapter.InstanceGroup{
						{
							Name:               "redis-server",
							VMType:             "dedicated-vm",
							VMExtensions:       []string{"dedicated-extensions"},
							PersistentDiskType: "dedicated-disk",
							Networks:           []string{"dedicated-network"},
							Instances:          45,
							AZs:                []string{"dedicated-az1", "dedicated-az2"},
						},
						{
							Name:         "health-check",
							VMType:       "health-check-vm",
							Lifecycle:    adapter.LifecycleErrandType,
							VMExtensions: []string{"health-check-extensions"},
							Networks:     []string{"health-check-network"},
							Instances:    1,
							AZs:          []string{"health-check-az1"},
						},
					},
				}
			})

			It("sets the health check instance group systest-sleep property when using a health check plan with systest_errand_sleep set", func() {
				plan.Properties["systest_errand_sleep"] = 5
				oldManifest := createDefaultOldManifest()

				generated, generateErr := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					plan,
					defaultRequestParameters,
					&oldManifest,
					nil,
					nil,
				)

				Expect(generateErr).NotTo(HaveOccurred())
				Expect(
					generated.Manifest.
						InstanceGroups[1].
						Properties[adapter.HealthCheckErrandName].(map[interface{}]interface{})["systest-sleep"],
				).To(Equal(5))
			})

			It("sets the health check instance group systest-failure-override property to true when using a failing health check plan", func() {
				plan.Properties["systest_errand_failure_override"] = adapter.HealthCheckErrandName
				oldManifest := createDefaultOldManifest()

				generated, generateErr := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					plan,
					defaultRequestParameters,
					&oldManifest,
					nil,
					nil,
				)

				Expect(generateErr).NotTo(HaveOccurred())
				Expect(
					generated.Manifest.
						InstanceGroups[1].
						Properties[adapter.HealthCheckErrandName].(map[interface{}]interface{})["systest-failure-override"],
				).To(Equal(true))
			})
		})

		It("sets the health check instance group systest-failure-override property to true when using a failing cleanup data plan", func() {
			plan := serviceadapter.Plan{
				Properties: map[string]interface{}{
					"persistence":                     false,
					"systest_errand_failure_override": adapter.CleanupDataErrandName,
				},
				InstanceGroups: []serviceadapter.InstanceGroup{
					{
						Name:               "redis-server",
						VMType:             "dedicated-vm",
						VMExtensions:       []string{"dedicated-extensions"},
						PersistentDiskType: "dedicated-disk",
						Networks:           []string{"dedicated-network"},
						Instances:          45,
						AZs:                []string{"dedicated-az1", "dedicated-az2"},
					},
					{
						Name:         "cleanup-data",
						VMType:       "cleanup-data-vm",
						Lifecycle:    adapter.LifecycleErrandType,
						VMExtensions: []string{"cleanup-data-extensions"},
						Networks:     []string{"cleanup-data-network"},
						Instances:    1,
						AZs:          []string{"cleanup-data-az1"},
					},
				},
			}

			oldManifest := createDefaultOldManifest()

			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				plan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(
				generated.Manifest.
					InstanceGroups[1].
					Properties[adapter.CleanupDataErrandName].(map[interface{}]interface{})["systest-failure-override"],
			).To(Equal(true))
		})

		It("uses that value in manifest properties when maxclients is set in arbitrary parameters", func() {
			requestParams := map[string]interface{}{
				"parameters": map[string]interface{}{
					"maxclients": 22.0, // This data comes directly from JSON unmarshalling: no integers allowed!
				},
			}

			oldManifest := createDefaultOldManifest()

			generated, _ := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				requestParams,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generated.Manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["maxclients"]).To(Equal(22))
		})

		It("uses that value in secrets map when odb_managed_secret is set in arbitrary parameters", func() {
			requestParams := map[string]interface{}{
				"parameters": map[string]interface{}{
					adapter.ManagedSecretKey: "foo",
				},
			}

			oldManifest := createDefaultOldManifest()

			generated, _ := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				requestParams,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generated.ODBManagedSecrets[adapter.ManagedSecretKey]).To(Equal("foo"))
		})

		It("set credhub reference if credhub_secret_path is set in arbitrary parameters", func() {
			requestParams := map[string]interface{}{
				"parameters": map[string]interface{}{
					"credhub_secret_path": "/foo",
				},
			}

			oldManifest := createDefaultOldManifest()

			generated, err := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				requestParams,
				&oldManifest,
				nil,
				nil,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(generated.Manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["secret"]).To(Equal("((/foo))"))
		})

		It("returns an error when invalid arbitrary parameters are set", func() {
			invalidRequestParams := map[string]interface{}{
				"parameters": map[string]interface{}{"maxclients": 22.0, "foo": "bar", "baz": "barry"},
			}

			oldManifest := createDefaultEmptyManifest()

			_, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				invalidRequestParams,
				&oldManifest,
				nil,
				nil,
			)
			Expect(generateErr).To(MatchError(ContainSubstring("foo")))
			Expect(generateErr).To(MatchError(ContainSubstring("baz")))
		})

		It("returns an error when the health-check job is missing from the service releases", func() {
			missingHealthCheckJobReleases := serviceadapter.ServiceReleases{
				{
					Name:    "some-release-name",
					Version: "4",
					Jobs: []string{
						ProvidedRedisServerInstanceGroupName,
						adapter.CleanupDataErrandName,
					},
				},
			}

			oldManifest := createDefaultOldManifest()

			_, generateErr := generateManifest(
				manifestGenerator,
				missingHealthCheckJobReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).To(HaveOccurred())
			Expect(generateErr).To(MatchError(fmt.Sprintf(
				"no release provided for job %s",
				adapter.HealthCheckErrandName,
			)))
		})

		It("returns an error when redis job is missing from the service releases", func() {
			oldManifest := createDefaultOldManifest()

			missingRedisJobRelease := serviceadapter.ServiceReleases{
				{
					Name:    "some-release-name",
					Version: "4",
					Jobs: []string{
						adapter.HealthCheckErrandName,
						adapter.CleanupDataErrandName,
					},
				},
			}

			_, generateErr := generateManifest(
				manifestGenerator,
				missingRedisJobRelease,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).To(HaveOccurred())
			Expect(generateErr).To(MatchError("no release provided for job redis-server"))
		})

		It("returns an error when the cleanup data job is missing from the service releases", func() {
			missingCleanupDataJobRelease := serviceadapter.ServiceReleases{
				{
					Name:    "some-release-name",
					Version: "4",
					Jobs: []string{
						ProvidedRedisServerInstanceGroupName,
						adapter.HealthCheckErrandName,
					},
				},
			}

			oldManifest := createDefaultOldManifest()

			_, generateErr := generateManifest(
				manifestGenerator,
				missingCleanupDataJobRelease,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).To(HaveOccurred())
			Expect(generateErr).To(MatchError(fmt.Sprintf(
				"no release provided for job %s",
				adapter.CleanupDataErrandName,
			)))
		})

		It("returns an error when a job is provided by 2 different releases", func() {
			multipleServiceReleases := append(defaultServiceReleases, serviceadapter.ServiceRelease{
				Name:    "some-other-release",
				Version: "some-version",
				Jobs:    defaultServiceReleases[0].Jobs,
			})

			oldManifest := createDefaultEmptyManifest()

			_, generateErr := generateManifest(
				manifestGenerator,
				multipleServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).To(MatchError(fmt.Sprintf("job %s defined in multiple releases: some-release-name, some-other-release", ProvidedRedisServerInstanceGroupName)))
		})

		It("returns an error with a message for the cli user when a plan does not have an instance group named redis-server", func() {
			planWithoutExpectedInstanceGroupName := serviceadapter.Plan{
				InstanceGroups: []serviceadapter.InstanceGroup{{Name: "not-redis-server"}},
			}

			oldManifest := createDefaultOldManifest()

			_, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				planWithoutExpectedInstanceGroupName,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generateErr).To(HaveOccurred())
			Expect(generateErr).To(MatchError(ContainSubstring("Contact your operator, service configuration issue occurred")))
			Expect(stderr).To(gbytes.Say("no redis-server instance group definition found"))
		})

		It("logs and returns an error when a plan does not define a required property", func() {
			oldManifest := createDefaultOldManifest()

			_, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				planWithPropertyRemoved(dedicatedPlan, "persistence"),
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)
			Expect(generateErr).To(HaveOccurred())
			Expect(generateErr).To(MatchError(""))
			Expect(stderr).To(gbytes.Say("the plan property 'persistence' is missing"))
		})

		It("returns an error when the new release version (of the release that provides redis-server) cannot be parsed", func() {
			defaultServiceReleases[0].Version = "oi"

			oldManifest := createDefaultOldManifest()

			_, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)
			Expect(generateErr).To(MatchError("oi is not a valid BOSH release version"))
		})

		It("returns an error when the old release version (of the release that provides redis-server) cannot be parsed", func() {
			oldManifest := createDefaultOldManifest()
			oldManifest.Releases[0].Version = "oi"

			_, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)
			Expect(generateErr).To(MatchError("oi is not a valid BOSH release version"))
		})

		It("returns an error when the old manifest does not contain any releases with the same name as the configured release that provides redis-server job", func() {
			oldManifest := createDefaultOldManifest()
			oldManifest.Releases[0].Name = "i-dont-exist-in-newer-config"

			_, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)
			Expect(generateErr).To(MatchError("no release with name some-release-name found in previous manifest"))
		})

		It("generates the expected manifest when the old manifest is valid", func() {
			oldManifest := createDefaultOldManifest()
			oldManifest.Releases[0].Version = "1"

			generated, _ := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			var expectedManifest bosh.BoshManifest
			expectedManifestRaw, _ := ioutil.ReadFile(getFixturePath("dedicated-plan-updated-manifest.yml"))
			err := yaml.Unmarshal(expectedManifestRaw, &expectedManifest)
			Expect(err).NotTo(HaveOccurred(), "Generated manifest not marshaled to yaml")

			removePlanSecret(generated.Manifest)
			Expect(generated.Manifest).To(Equal(expectedManifest))
		})

		It("generates the expected manifest when the old manifest is valid and ignores appended commit hash", func() {
			oldManifest := createDefaultOldManifest()
			oldManifest.Releases[0].Version = "0.1+dev.8-fa37909"

			generated, _ := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)

			var expectedManifest bosh.BoshManifest
			expectedManifestRaw, _ := ioutil.ReadFile(getFixturePath("dedicated-plan-updated-manifest.yml"))
			err := yaml.Unmarshal(expectedManifestRaw, &expectedManifest)
			Expect(err).NotTo(HaveOccurred(), "Generated manifest not marshaled to yaml")

			removePlanSecret(generated.Manifest)
			Expect(generated.Manifest).To(Equal(expectedManifest))
		})

		It("generates the expected manifest when arbitrary parameters are present that clash with values in the valid old manifest", func() {
			oldManifest := createDefaultOldManifest()
			oldManifest.Releases[0].Version = "1"

			generated, _ := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				map[string]interface{}{
					"parameters": map[string]interface{}{
						"maxclients": 56.0, // From JSON. No integers.
					},
				},
				&oldManifest,
				nil,
				nil,
			)

			var expectedManifest bosh.BoshManifest
			expectedManifestRaw, _ := ioutil.ReadFile(getFixturePath("dedicated-plan-updated-manifest-arbitrary-params.yml"))
			err := yaml.Unmarshal(expectedManifestRaw, &expectedManifest)
			Expect(err).NotTo(HaveOccurred(), "Generated manifest not marshaled to yaml")

			removePlanSecret(generated.Manifest)
			Expect(generated.Manifest).To(Equal(expectedManifest))
		})

		It("generates the expected manifest when an instance group has been migrated", func() {
			oldManifest := createDefaultOldManifest()

			manifestGenerator.Config.RedisInstanceGroupName = "redis"

			updatedDedicatedPlan := dedicatedPlan
			updatedDedicatedPlan.InstanceGroups[0].Name = "redis"
			updatedDedicatedPlan.InstanceGroups[0].MigratedFrom = []serviceadapter.Migration{
				{Name: "redis-server"},
			}

			generatedManifest, generatedErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				updatedDedicatedPlan,
				map[string]interface{}{},
				&oldManifest,
				nil,
				nil,
			)

			Expect(generatedErr).ToNot(HaveOccurred())
			Expect(generatedManifest.Manifest.InstanceGroups[0].Name).To(Equal("redis"))
			Expect(generatedManifest.Manifest.InstanceGroups[0].MigratedFrom[0].Name).To(Equal("redis-server"))
		})

		It("returns an error when an unknown instance group name has been configured", func() {
			oldManifest := createDefaultOldManifest()

			manifestGenerator.Config.RedisInstanceGroupName = "foo"

			updatedDedicatedPlan := dedicatedPlan
			updatedDedicatedPlan.InstanceGroups[0].Name = "redis"
			updatedDedicatedPlan.InstanceGroups[0].MigratedFrom = []serviceadapter.Migration{
				{Name: "redis-server"},
			}

			_, generatedErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				updatedDedicatedPlan,
				map[string]interface{}{},
				&oldManifest,
				nil,
				nil,
			)

			Expect(generatedErr).To(HaveOccurred())
			Expect(generatedErr).To(MatchError("Contact your operator, service configuration issue occurred"))
			Expect(stderr).To(gbytes.Say("no foo instance group definition found"))
		})

		It("returns an error when the redis server job does not have a release", func() {
			serviceReleaseWithMissingJobName := defaultServiceReleases
			serviceReleaseWithMissingJobName[0].Jobs = []string{"overrides-redis-server", "health-check", "cleanup-data"}

			_, generatedErr := generateManifest(
				manifestGenerator,
				serviceReleaseWithMissingJobName,
				dedicatedPlan,
				map[string]interface{}{},
				nil,
				nil,
				nil,
			)
			Expect(generatedErr).To(MatchError("error gathering redis server job: no release provided for job redis-server"))
		})

		It("sets the expected update block when the plan update block is empty and old manifest does not exist", func() {
			planWithoutUpdateBlock := dedicatedPlan
			planWithoutUpdateBlock.Update = nil

			generatedManifest, generatedErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				planWithoutUpdateBlock,
				map[string]interface{}{},
				nil,
				nil,
				nil,
			)

			Expect(generatedErr).ToNot(HaveOccurred())
			Expect(generatedManifest.Manifest.Update.MaxInFlight).To(Equal(4))
			Expect(generatedManifest.Manifest.Update.Canaries).To(Equal(4))
			Expect(generatedManifest.Manifest.Update.VmStrategy).To(Equal("delete-create"))
		})

		It("sets the secret property using the old manifest value when credhub_secret_path not present in arbitrary parameters", func() {
			oldManifest := createDefaultOldManifest()
			oldManifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["secret"] = "/some/special/path"
			emptyArbitraryParams := map[string]interface{}{}

			generatedManifest, generatedErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				emptyArbitraryParams,
				&oldManifest,
				nil,
				nil,
			)

			Expect(generatedErr).ToNot(HaveOccurred())
			Expect(generatedManifest.Manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["secret"]).To(Equal("/some/special/path"))
		})

		It("sets the expected update block when the plan update block is empty and old manifest exists", func() {
			oldManifest := createDefaultOldManifest()

			planWithoutUpdateBlock := dedicatedPlan
			planWithoutUpdateBlock.Update = nil

			generatedManifest, generatedErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				planWithoutUpdateBlock,
				map[string]interface{}{},
				&oldManifest,
				nil,
				nil,
			)

			Expect(generatedErr).ToNot(HaveOccurred())
			Expect(generatedManifest.Manifest.Update.Canaries).To(Equal(1))
			Expect(generatedManifest.Manifest.Update.MaxInFlight).To(Equal(1))
			Expect(generatedManifest.Manifest.Update.VmStrategy).To(Equal("delete-create"))
		})

		It("does not generate a manifest with odb prefix when previous manifest contains an existing credhub name", func() {
			oldManifest := createDefaultOldManifest()
			oldManifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})[adapter.ManagedSecretKey] = "((/odb/generated/path/yeee))"

			manifestOutput, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
				nil,
			)
			Expect(generateErr).ToNot(HaveOccurred())
			odbManagedSecret := manifestOutput.Manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})[adapter.ManagedSecretKey]
			Expect(odbManagedSecret.(string)).To(Equal("((/odb/generated/path/yeee))"))
		})

		It("does not retrieve ODB managed secret from previous manifest when IgnoreODBManagedSecretOnUpdate config flag is true", func() {
			oldManifest := createDefaultOldManifest()
			oldManifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})[adapter.ManagedSecretKey] = "((/odb/generated/path/yeee))"

			manifestGenerator.Config.IgnoreODBManagedSecretOnUpdate = true

			manifestOutput, generatedErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				map[string]interface{}{},
				&oldManifest,
				nil,
				nil,
			)

			Expect(generatedErr).NotTo(HaveOccurred())
			odbManagedSecret := manifestOutput.Manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})[adapter.ManagedSecretKey]
			Expect(odbManagedSecret.(string)).To(Equal("((" + serviceadapter.ODBSecretPrefix + ":" + adapter.ManagedSecretKey + "))"))
		})

		Describe("when regenerating a manifest that contains a ODB managed secret", func() {
			var (
				oldManifest    bosh.BoshManifest
				planWithSecret serviceadapter.Plan
			)

			BeforeEach(func() {
				oldManifest = createDefaultOldManifest()
				oldManifest.InstanceGroups[0].Name = config.RedisInstanceGroupName
				oldManifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["plan_secret"] = "((/odb/managed/path/to/plan_secret))"

				planWithSecret = serviceadapter.Plan{
					Properties: map[string]interface{}{
						"plan_secret": "plansecret",
						"persistence": true,
					},
					InstanceGroups: []serviceadapter.InstanceGroup{{Name: config.RedisInstanceGroupName}},
				}
			})

			getSecretKey := func(secrets serviceadapter.ODBManagedSecrets, expectedSecret string) string {
				for key, val := range secrets {
					if val == expectedSecret {
						return key
					}
				}
				return ""
			}

			It("reuses the credhub path from the old manifest when the secret is unchanged", func() {
				existingSecretCredhubPath := "((/odb/managed/path/to/plan_secret))"
				oldSecrets := serviceadapter.ManifestSecrets{existingSecretCredhubPath: "plansecret"}
				manifestOutput, err := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					planWithSecret,
					map[string]interface{}{},
					&oldManifest,
					&planWithSecret,
					oldSecrets,
				)
				Expect(err).NotTo(HaveOccurred())

				actualSecret := manifestOutput.Manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["plan_secret"]
				Expect(actualSecret).To(Equal(existingSecretCredhubPath))

				_, isSet := manifestOutput.ODBManagedSecrets["plan_secret"]
				Expect(isSet).To(BeFalse())
			})

			It("inserts a new odb_secret when the secret has changed", func() {
				existingSecretCredhubPath := "((/odb/managed/path/to/plan_secret))"
				oldSecrets := serviceadapter.ManifestSecrets{existingSecretCredhubPath: "different_secret"}
				manifestOutput, err := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					planWithSecret,
					map[string]interface{}{},
					&oldManifest,
					&planWithSecret,
					oldSecrets,
				)
				Expect(err).NotTo(HaveOccurred())

				actualSecret := manifestOutput.Manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["plan_secret"]

				secretKey := getSecretKey(manifestOutput.ODBManagedSecrets, "plansecret")
				Expect(secretKey).ToNot(BeEmpty())
				Expect(actualSecret).To(Equal("((odb_secret:" + secretKey + "))"))
				Expect(manifestOutput.ODBManagedSecrets[secretKey]).To(Equal("plansecret"))
			})

			It("does not reuse the odb secret name when regenerating a secret", func() {
				provisionManifestOutput, err := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					planWithSecret,
					map[string]interface{}{},
					nil,
					nil,
					nil,
				)
				Expect(err).NotTo(HaveOccurred())
				provisionSecretKey := getSecretKey(provisionManifestOutput.ODBManagedSecrets, "plansecret")

				existingSecretCredhubPath := "((/odb/managed/path/to/plan_secret))"
				oldSecrets := serviceadapter.ManifestSecrets{existingSecretCredhubPath: "different_secret"}
				updateManifestOutput, err := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					planWithSecret,
					map[string]interface{}{},
					&oldManifest,
					&planWithSecret,
					oldSecrets,
				)
				Expect(err).NotTo(HaveOccurred())
				updateSecretKey := getSecretKey(updateManifestOutput.ODBManagedSecrets, "plansecret")

				Expect(provisionSecretKey).ToNot(Equal(updateSecretKey))
			})

			It("does not include the secret in the manifest nor in the secrets map when not supplied via plan properties", func() {
				planWithoutSecret := serviceadapter.Plan{
					Properties: map[string]interface{}{
						"persistence": true,
					},
					InstanceGroups: []serviceadapter.InstanceGroup{{Name: config.RedisInstanceGroupName}},
				}
				provisionManifestOutput, err := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					planWithoutSecret,
					map[string]interface{}{},
					nil,
					nil,
					nil,
				)
				Expect(err).NotTo(HaveOccurred())
				_, found := provisionManifestOutput.Manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["plan_secret"]
				Expect(found).To(BeFalse(), "should not have plan_secret key in manifest")
				for key, _ := range provisionManifestOutput.ODBManagedSecrets {
					Expect(key).ToNot(HavePrefix("plan_secret_key"))
				}
			})

		})

		Describe("release version tests", func() {
			type testInputs struct {
				oldVersion   string
				newVersion   string
				returnsError bool
			}

			runReleaseVersionTests := func(t testInputs) {
				errorString := fmt.Sprintf("error generating manifest: new release version %s is lower than existing release version %s", t.newVersion, t.oldVersion)

				Context(fmt.Sprintf("when the old version (of the release that provides redis-server) is %s and the new version is %s", t.oldVersion, t.newVersion), func() {
					var itStatement string

					if t.returnsError {
						itStatement = "returns the expected error"
					} else {
						itStatement = "returns no error"
					}

					It(itStatement, func() {
						defaultServiceReleases[0].Version = t.newVersion

						oldManifest := createDefaultOldManifest()
						oldManifest.Releases[0].Version = t.oldVersion

						_, generateErr := generateManifest(
							manifestGenerator,
							defaultServiceReleases,
							dedicatedPlan,
							defaultRequestParameters,
							&oldManifest,
							nil,
							nil,
						)
						if t.returnsError {
							Expect(generateErr).To(MatchError(errorString))
						} else {
							Expect(generateErr).NotTo(HaveOccurred())
						}
					})
				})
			}

			for _, t := range []testInputs{
				{"3", "3", false}, {"3", "4", false}, {"3", "2", true},
				{"3.1", "3.1", false}, {"3.2", "3.10", false}, {"3.10", "3.2", true},
				{"3", "3.0", false}, {"3.0", "3", false},
				{"3", "3.1", false}, {"3.1", "3", true},
				{"0+dev.2", "0+dev.2", false}, {"0+dev.2", "0+dev.10", false}, {"0+dev.10", "0+dev.2", true},
				{"1.1+dev.2", "1.1+dev.2", false}, {"1.1+dev.2", "1.1+dev.10", false}, {"1.1+dev.10", "1.1+dev.2", true},
				{"1.2", "2.1", false}, {"2.1", "1.2", true},
				{"1+dev.2", "2+dev.1", false}, {"2+dev.1", "1+dev.2", true},
				{"0.1+dev.2", "0.2+dev.1", false}, {"0.2+dev.1", "0.1+dev.2", true},
				{"0+dev.1", "1", false}, {"1", "1+dev.1", false}, {"2", "1+dev.1", true},
				{"latest", "latest", false},
			} {
				runReleaseVersionTests(t)
			}

		})
	})

})

func createDefaultEmptyManifest() bosh.BoshManifest {
	return bosh.BoshManifest{}
}

func createDefaultOldManifest() bosh.BoshManifest {
	return bosh.BoshManifest{
		Releases: []bosh.Release{
			{Name: "some-release-name", Version: "4"},
		},
		InstanceGroups: []bosh.InstanceGroup{
			{Properties: map[string]interface{}{
				"redis": map[interface{}]interface{}{
					"password":    "some-password",
					"persistence": "this is the old value",
					"maxclients":  47,
				},
			}}},
	}
}

func planWithPropertyRemoved(plan serviceadapter.Plan, property string) serviceadapter.Plan {
	propertySlice := strings.Split(property, ".")
	if len(propertySlice) == 1 {
		delete(plan.Properties, property)
	} else {
		delete(plan.Properties[propertySlice[0]].(map[string]interface{}), propertySlice[1])
	}
	return plan
}

func generateManifest(
	manifestGenerator adapter.ManifestGenerator,
	serviceReleases serviceadapter.ServiceReleases,
	plan serviceadapter.Plan,
	requestParams map[string]interface{},
	oldManifest *bosh.BoshManifest,
	oldPlan *serviceadapter.Plan,
	oldSecrets serviceadapter.ManifestSecrets,
) (serviceadapter.GenerateManifestOutput, error) {

	return manifestGenerator.GenerateManifest(serviceadapter.ServiceDeployment{
		DeploymentName: "some-instance-id",
		Stemcell: serviceadapter.Stemcell{
			OS:      "some-stemcell-os",
			Version: "1234",
		},
		Releases: serviceReleases,
	}, plan, requestParams, oldManifest, oldPlan, oldSecrets)
}

func containsJobName(list []bosh.Job, query string) bool {
	for _, v := range list {
		if v.Name == query {
			return true
		}
	}
	return false
}

func removePlanSecret(manifest bosh.BoshManifest) {
	delete(manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{}), "plan_secret")
}
