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
	"os"
	"path/filepath"

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
		binder                   adapter.Binder
		dedicatedPlan            serviceadapter.Plan
		highMemoryPlan           serviceadapter.Plan
		stderr                   *gbytes.Buffer
		stderrLogger             *log.Logger
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

		manifestGenerator = createManifestGenerator("redis-example-service-adapter.conf", stderrLogger)

		binder = adapter.Binder{StderrLogger: stderrLogger}
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
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(
				generated.
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
				)
				Expect(generateErr).NotTo(HaveOccurred())
				Expect(stderr).NotTo(gbytes.Say(`Non Cloud Foundry platform \(or pre OSBAPI 2\.13\) detected`))
			})

		})

		It("contains only one instance group and multiple jobs, when `colocated_errand` property is set to true and post_deploy has been configured", func() {
			oldManifest := createDefaultOldManifest()

			plan := serviceadapter.Plan{
				LifecycleErrands: serviceadapter.LifecycleErrands{
					PostDeploy: serviceadapter.Errand{
						Name:      "health-check",
						Instances: []string{"redis-server"},
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

			colocatedPostDeployPlan := plan
			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				colocatedPostDeployPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(containsJobName(generated.InstanceGroups[0].Jobs, "redis-server")).To(BeTrue())
			Expect(containsJobName(generated.InstanceGroups[0].Jobs, "health-check")).To(BeTrue())
			Expect(generated.InstanceGroups[0].Jobs).To(HaveLen(2))
		})

		It("contains only one instance group and multiple jobs, when `colocated_errand` property is set to true and pre_delete has been configured", func() {
			oldManifest := createDefaultOldManifest()

			plan := serviceadapter.Plan{
				LifecycleErrands: serviceadapter.LifecycleErrands{
					PreDelete: []serviceadapter.Errand{{
						Name:      "cleanup-data",
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

			colocatedPreDeletePlan := plan
			generated, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				colocatedPreDeletePlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(containsJobName(generated.InstanceGroups[0].Jobs, "redis-server")).To(BeTrue())
			Expect(containsJobName(generated.InstanceGroups[0].Jobs, "cleanup-data")).To(BeTrue())
			Expect(generated.InstanceGroups[0].Jobs).To(HaveLen(2))
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
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(generated.Features.UseShortDNSAddresses).ToNot(BeNil())
			Expect(*generated.Features.UseShortDNSAddresses).To(BeTrue())
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
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(generated.Features.UseShortDNSAddresses).ToNot(BeNil())
			Expect(*generated.Features.UseShortDNSAddresses).To(BeFalse())
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
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(generated.Features.UseShortDNSAddresses).To(BeNil())
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
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(generated.Features.ExtraFeatures).To(Equal(map[string]interface{}{
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
				)

				Expect(generateErr).NotTo(HaveOccurred())
				Expect(
					generated.
						InstanceGroups[1].
						Properties[adapter.HealthCheckErrandName].(map[interface{}]interface{})["systest-sleep"],
				).To(Equal(5))
			})

			It("sets the health check instance group systest-failure-override property to true when using a failing health check plan", func() {
				plan.Properties["systest_errand_failure_override"] = true
				oldManifest := createDefaultOldManifest()

				generated, generateErr := generateManifest(
					manifestGenerator,
					defaultServiceReleases,
					plan,
					defaultRequestParameters,
					&oldManifest,
					nil,
				)

				Expect(generateErr).NotTo(HaveOccurred())
				Expect(
					generated.
						InstanceGroups[1].
						Properties[adapter.HealthCheckErrandName].(map[interface{}]interface{})["systest-failure-override"],
				).To(Equal(true))
			})
		})

		It("sets the health check instance group systest-failure-override property to true when using a failing cleanup data plan", func() {
			plan := serviceadapter.Plan{
				Properties: map[string]interface{}{
					"persistence":                     false,
					"systest_errand_failure_override": true,
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
			)

			Expect(generateErr).NotTo(HaveOccurred())
			Expect(
				generated.
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
			)

			Expect(generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["maxclients"]).To(Equal(22))
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
			)
			Expect(generateErr).To(HaveOccurred())
			Expect(generateErr).To(MatchError(""))
			Expect(stderr).To(gbytes.Say("the plan property 'persistence' is missing"))
		})

		It("logs and returns an error when the configuration does not exists", func() {
			oldManifest := createDefaultOldManifest()

			manifestGenerator = createManifestGenerator("/foobar.conf", stderrLogger)

			_, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
			)

			Expect(stderr).To(gbytes.Say(fmt.Sprintf("Error reading config file from %s", manifestGenerator.ConfigPath)))
			Expect(generateErr).To(HaveOccurred())
			Expect(generateErr).To(MatchError(fmt.Sprintf("Error reading config file from %s", manifestGenerator.ConfigPath)))

		})

		It("logs and returns an error when the configuration is not valid YML", func() {
			oldManifest := createDefaultOldManifest()

			manifestGenerator = createManifestGenerator("broken-redis-example-service-adapter.conf", stderrLogger)

			_, generateErr := generateManifest(
				manifestGenerator,
				defaultServiceReleases,
				dedicatedPlan,
				defaultRequestParameters,
				&oldManifest,
				nil,
			)

			Expect(stderr).To(gbytes.Say("Error unmarshalling config"))
			Expect(generateErr).To(HaveOccurred())
			Expect(generateErr).To(MatchError("Error unmarshalling config"))

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
			)

			out, err := yaml.Marshal(generated)
			Expect(err).NotTo(HaveOccurred(), "Generated manifest not marshaled to yaml")

			expectedManifest, _ := ioutil.ReadFile(getFixturePath("dedicated-plan-updated-manifest.yml"))

			Expect(out).To(Equal(expectedManifest))
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
			)

			out, err := yaml.Marshal(generated)
			Expect(err).NotTo(HaveOccurred(), "Generated manifest not marshaled to yaml")

			expectedManifest, _ := ioutil.ReadFile(getFixturePath("dedicated-plan-updated-manifest.yml"))

			Expect(out).To(Equal(expectedManifest))
		})

		It("generates the expected manifest when when arbitrary parameters are present that clash with values in the valid old manifest", func() {
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
			)

			out, err := yaml.Marshal(generated)
			Expect(err).NotTo(HaveOccurred(), "Generated manifest not marshaled to yaml")

			expectedManifest, _ := ioutil.ReadFile(getFixturePath("dedicated-plan-updated-manifest-arbitrary-params.yml"))

			Expect(out).To(Equal(expectedManifest))
		})

		It("generates the expected manifest when an instance group has been migrated", func() {
			oldManifest := createDefaultOldManifest()

			manifestGenerator = createManifestGenerator("redis-example-service-adapter-updated.conf", stderrLogger)

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
			)

			Expect(generatedErr).ToNot(HaveOccurred())
			Expect(generatedManifest.InstanceGroups[0].Name).To(Equal("redis"))
			Expect(generatedManifest.InstanceGroups[0].MigratedFrom[0].Name).To(Equal("redis-server"))
		})

		It("returns an error when an unknown instance group name has been configured", func() {
			oldManifest := createDefaultOldManifest()

			manifestGenerator = createManifestGenerator("redis-example-service-adapter-missing.conf", stderrLogger)

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
			)

			Expect(generatedErr).To(HaveOccurred())
			Expect(generatedErr).To(MatchError("Contact your operator, service configuration issue occurred"))
			Expect(stderr).To(gbytes.Say("no foo instance group definition found"))
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
			)

			Expect(generatedErr).ToNot(HaveOccurred())
			Expect(generatedManifest.Update.MaxInFlight).To(Equal(4))
			Expect(generatedManifest.Update.Canaries).To(Equal(4))
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
			)

			Expect(generatedErr).ToNot(HaveOccurred())
			Expect(generatedManifest.Update.Canaries).To(Equal(1))
			Expect(generatedManifest.Update.MaxInFlight).To(Equal(1))
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

	Describe("binding", func() {
		var (
			actualBinding    serviceadapter.Binding
			actualBindingErr error
			expectedPassword = "expectedPassword"
			boshVMs          bosh.BoshVMs
			currentManifest  bosh.BoshManifest
		)
		BeforeEach(func() {

			boshVMs = bosh.BoshVMs{"redis-server": []string{"an-ip"}}
			currentManifest = bosh.BoshManifest{
				InstanceGroups: []bosh.InstanceGroup{
					{
						Properties: map[string]interface{}{"redis": map[interface{}]interface{}{"password": expectedPassword}},
					},
				},
			}

		})

		JustBeforeEach(func() {
			actualBinding, actualBindingErr = binder.CreateBinding("not-relevant", boshVMs, currentManifest, nil)
		})

		Context("has a password in the manifest", func() {
			It("has no error", func() {
				Expect(actualBindingErr).NotTo(HaveOccurred())
			})

			It("returns the password from the manifest", func() {
				Expect(actualBinding.Credentials["password"]).To(Equal(expectedPassword))
			})

			It("returns the host from the vms", func() {
				Expect(actualBinding.Credentials["host"]).To(Equal("an-ip"))
			})
		})

		Context("when the bosh vms don't have redis-server", func() {
			BeforeEach(func() {
				boshVMs = bosh.BoshVMs{"redis-server1": []string{"an-ip"}}
			})
			It("returns an error for the cli user", func() {
				Expect(actualBindingErr).To(HaveOccurred())
				Expect(actualBindingErr).To(MatchError(""))
			})
			It("logs an error for the operator", func() {
				Expect(stderr).To(gbytes.Say("expected redis-server instance group to have only 1 instance, got 0"))
			})
		})

		Context("when the bosh vms has a redis-server key, but it has no instances", func() {
			BeforeEach(func() {
				boshVMs = bosh.BoshVMs{"redis-server": []string{}}
			})
			It("returns an error for the cli user", func() {
				Expect(actualBindingErr).To(HaveOccurred())
				Expect(actualBindingErr).To(MatchError(""))
			})
			It("logs an error for the operator", func() {
				Expect(stderr).To(gbytes.Say("expected redis-server instance group to have only 1 instance, got 0"))
			})
		})

		Context("when there are no instance groups for Redis", func() {
			BeforeEach(func() {
				boshVMs = bosh.BoshVMs{}
			})
			It("returns an error for the cli user", func() {
				Expect(actualBindingErr).To(HaveOccurred())
				Expect(actualBindingErr).To(MatchError(""))
			})
			It("logs an error for the operator", func() {
				Expect(stderr).To(gbytes.Say("expected 1 instance group in the Redis deployment, got 0"))
			})
		})

	})
})

func createManifestGenerator(filename string, logger *log.Logger) adapter.ManifestGenerator {
	return adapter.ManifestGenerator{
		StderrLogger: logger,
		ConfigPath:   getFixturePath(filename),
	}
}

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

func getFixturePath(filename string) string {
	cwd, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	return filepath.Join(cwd, "fixtures", filename)
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
) (bosh.BoshManifest, error) {

	return manifestGenerator.GenerateManifest(serviceadapter.ServiceDeployment{
		DeploymentName: "some-instance-id",
		Stemcell: serviceadapter.Stemcell{
			OS:      "some-stemcell-os",
			Version: "1234",
		},
		Releases: serviceReleases,
	}, plan, requestParams, oldManifest, oldPlan)
}

func containsJobName(list []bosh.Job, query string) bool {
	for _, v := range list {
		if v.Name == query {
			return true
		}
	}
	return false
}
