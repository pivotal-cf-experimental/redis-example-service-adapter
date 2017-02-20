package adapter_test

import (
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/pivotal-cf-experimental/redis-example-service-adapter/adapter"
	"github.com/pivotal-cf/on-demand-services-sdk/bosh"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

func nestedMapValue(m interface{}, keys ...string) interface{} {
	if len(keys) == 1 {
		return readMapWithStringOrInterfaceKey(m, keys[0])
	}
	return nestedMapValue(readMapWithStringOrInterfaceKey(m, keys[0]), keys[1:]...)
}

func readMapWithStringOrInterfaceKey(m interface{}, key string) interface{} {
	if m == nil {
		Fail(fmt.Sprintf("key %s does not exist", key))
	}

	switch m := m.(type) {
	case map[string]interface{}:
		return m[key]
	case map[interface{}]interface{}:
		return m[key]
	}
	Fail("can only work with map[string]interface{} or map[interface{}]interface{}")
	return nil
}

var _ = Describe("Redis Service Adapter", func() {
	var (
		plan              serviceadapter.Plan
		serviceReleases   serviceadapter.ServiceReleases
		manifestGenerator adapter.ManifestGenerator
		binder            adapter.Binder
		dedicatedPlan     serviceadapter.Plan
		highMemoryPlan    serviceadapter.Plan
		stderr            *gbytes.Buffer
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

		plan = dedicatedPlan
		serviceReleases = serviceadapter.ServiceReleases{
			{Name: "some-release-name", Version: "4", Jobs: []string{adapter.RedisServerJobName, adapter.HealthCheckErrandName}},
		}

		stderr = gbytes.NewBuffer()
		stderrLogger := log.New(io.MultiWriter(stderr, GinkgoWriter), "", log.LstdFlags)

		manifestGenerator = adapter.ManifestGenerator{StderrLogger: stderrLogger}
		binder = adapter.Binder{StderrLogger: stderrLogger}
	})

	Describe("Generating manifests", func() {
		var (
			oldManifest   *bosh.BoshManifest
			requestParams map[string]interface{}

			generated   bosh.BoshManifest
			generateErr error
		)

		BeforeEach(func() {
			oldManifest = nil
			requestParams = map[string]interface{}{}
		})

		JustBeforeEach(func() {
			adapter.CurrentPasswordGenerator = func() (string, error) {
				return "really random password", nil
			}
			generated, generateErr = manifestGenerator.GenerateManifest(serviceadapter.ServiceDeployment{
				DeploymentName: "some-instance-id",
				Stemcell: serviceadapter.Stemcell{
					OS:      "some-stemcell-os",
					Version: "1234",
				},
				Releases: serviceReleases,
			}, plan, requestParams, oldManifest, &dedicatedPlan)
		})

		It("returns no error", func() {
			Expect(generateErr).NotTo(HaveOccurred())
		})

		Describe("dedicated plan", func() {
			It("has the deployment name", func() {
				Expect(generated.Name).To(Equal("some-instance-id"))
			})

			It("has the service release", func() {
				Expect(generated.Releases).To(ConsistOf(
					bosh.Release{Name: "some-release-name", Version: "4"},
				))
			})

			It("has the service stemcell", func() {
				Expect(generated.Stemcells).To(HaveLen(1))
				Expect(generated.Stemcells[0].OS).To(Equal("some-stemcell-os"))
				Expect(generated.Stemcells[0].Version).To(Equal("1234"))
			})

			It("has two instance groups", func() {
				Expect(generated.InstanceGroups).To(HaveLen(2))
			})

			It("has a redis-server instance group", func() {
				Expect(generated.InstanceGroups[0].Name).To(Equal("redis-server"))
				Expect(generated.InstanceGroups[0].Instances).To(Equal(45))
				Expect(generated.InstanceGroups[0].Lifecycle).To(BeEmpty())

				Expect(generated.InstanceGroups[0].Jobs).To(ConsistOf(
					bosh.Job{Name: adapter.RedisServerJobName, Release: "some-release-name"},
				))

				Expect(generated.InstanceGroups[0].VMType).To(Equal("dedicated-vm"))
				Expect(generated.InstanceGroups[0].VMExtensions).To(ConsistOf("dedicated-extensions"))
				Expect(generated.InstanceGroups[0].PersistentDiskType).To(Equal("dedicated-disk"))
				Expect(generated.InstanceGroups[0].Networks).To(ConsistOf(bosh.Network{Name: "dedicated-network"}))
				Expect(generated.InstanceGroups[0].AZs).To(ConsistOf("dedicated-az1", "dedicated-az2"))
			})

			It("has properties for redis-server", func() {
				instanceGroupRedisProperties := generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})
				Expect(instanceGroupRedisProperties["password"]).To(Equal("really random password"))
				Expect(instanceGroupRedisProperties["persistence"]).To(Equal("yes"))
				Expect(instanceGroupRedisProperties["maxclients"]).To(Equal(10000))
			})

			It("has a health-check errand", func() {
				Expect(generated.InstanceGroups[1].Name).To(Equal("health-check"))
				Expect(generated.InstanceGroups[1].Instances).To(Equal(1))
				Expect(generated.InstanceGroups[1].Jobs).To(ConsistOf(
					bosh.Job{Name: "health-check", Release: "some-release-name"},
				))
				Expect(generated.InstanceGroups[1].Lifecycle).To(Equal(adapter.LifecycleErrandType))
				Expect(generated.InstanceGroups[1].VMType).To(Equal("health-check-vm"))
				Expect(generated.InstanceGroups[1].VMExtensions).To(ConsistOf("health-check-extensions"))
				Expect(generated.InstanceGroups[1].PersistentDiskType).To(BeEmpty())
				Expect(generated.InstanceGroups[1].Networks).To(ConsistOf(bosh.Network{Name: "health-check-network"}))
				Expect(generated.InstanceGroups[1].AZs).To(ConsistOf("health-check-az1"))
			})

			It("has an update block", func() {
				Expect(generated.Update).To(Equal(bosh.Update{
					Canaries:        1,
					CanaryWatchTime: "100-200",
					UpdateWatchTime: "100-200",
					MaxInFlight:     5,
					Serial:          nil,
				}))
			})

			It("does not set the health check instance group systest-failure-override property", func() {
				Expect(generated.InstanceGroups[1].Properties["health-check"]).To(
					BeNil(),
				)
			})

			It("returns no error", func() {
				Expect(generateErr).NotTo(HaveOccurred())
			})
		})

		Describe("high memory plan", func() {
			BeforeEach(func() {
				plan = highMemoryPlan
			})

			It("returns no error", func() {
				Expect(generateErr).NotTo(HaveOccurred())
			})

			It("sets the instance group's redis persistence property to be 'no'", func() {
				Expect(
					generated.
						InstanceGroups[0].
						Properties["redis"].(map[interface{}]interface{})["persistence"],
				).To(Equal("no"))
			})
		})

		Describe("failing health check plan", func() {
			BeforeEach(func() {
				plan = serviceadapter.Plan{
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

			It("returns no error", func() {
				Expect(generateErr).NotTo(HaveOccurred())
			})

			It("sets the health check instance group systest-failure-override property to true", func() {
				Expect(
					generated.
						InstanceGroups[1].
						Properties["health-check"].(map[interface{}]interface{})["systest-failure-override"],
				).To(Equal(true))
			})
		})

		Context("when config_server is enabled", func() {
			BeforeEach(func() {
				plan.Properties["config_server_manifest"] = true
			})

			It("returns no error", func() {
				Expect(generateErr).NotTo(HaveOccurred())
			})

			It("sets the redis password manifest property as a config server token", func() {
				instanceGroupRedisProperties := generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})
				Expect(instanceGroupRedisProperties["password"]).To(Equal("((redis-server-password))"))
			})
		})

		Context("when config_server is disabled", func() {
			BeforeEach(func() {
				plan.Properties["config_server_manifest"] = false
			})

			It("returns no error", func() {
				Expect(generateErr).NotTo(HaveOccurred())
			})

			It("sets the redis password manifest property as a config server token", func() {
				instanceGroupRedisProperties := generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})
				Expect(instanceGroupRedisProperties["password"]).To(Equal("really random password"))
			})
		})

		Context("when maxclients is set in arbitrary parameters", func() {
			BeforeEach(func() {
				requestParams = map[string]interface{}{
					"parameters": map[string]interface{}{
						"maxclients": 22.0, // This data comes directly from JSON unmarshalling: no integers allowed!
					},
				}
			})

			It("uses that value in manifest properties", func() {
				Expect(generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["maxclients"]).To(Equal(22))
			})
		})

		Context("when invalid arbitrary parameters are set", func() {
			BeforeEach(func() {
				requestParams = map[string]interface{}{
					"parameters": map[string]interface{}{"maxclients": 22.0, "foo": "bar", "baz": "barry"},
				}
			})

			It("returns an error", func() {
				Expect(generateErr).To(MatchError(ContainSubstring("foo")))
				Expect(generateErr).To(MatchError(ContainSubstring("baz")))
			})
		})

		Context("when the health-check job is missing from the service releases", func() {
			BeforeEach(func() {
				serviceReleases = serviceadapter.ServiceReleases{
					{Name: "some-release-name", Version: "4", Jobs: []string{adapter.RedisServerJobName}},
				}
			})

			It("returns an error", func() {
				Expect(generateErr).To(HaveOccurred())
				Expect(generateErr).To(MatchError(fmt.Sprintf("no release provided for job %s", adapter.HealthCheckErrandName)))
			})
		})

		Context("when a job is provided by 2 different releases", func() {
			BeforeEach(func() {
				serviceReleases = append(serviceReleases, serviceadapter.ServiceRelease{
					Name:    "some-other-release",
					Version: "some-version",
					Jobs:    serviceReleases[0].Jobs,
				})
			})

			It("returns an error", func() {
				Expect(generateErr).To(MatchError(fmt.Sprintf("job %s defined in multiple releases: some-release-name, some-other-release", adapter.RedisServerJobName)))
			})
		})

		Context("when an old manifest exists", func() {
			BeforeEach(func() {
				oldManifest = &bosh.BoshManifest{
					Releases: []bosh.Release{
						{Name: "some-release-name", Version: "remember-to-set-me"},
					},
					InstanceGroups: []bosh.InstanceGroup{
						bosh.InstanceGroup{Properties: map[string]interface{}{"redis": map[interface{}]interface{}{
							"password":    "some-password",
							"persistence": "this is the old value",
							"maxclients":  47,
						}}}},
				}
			})

			type testInputs struct {
				oldVersion   string
				newVersion   string
				returnsError bool
			}

			runReleaseVersionTests := func(t testInputs) {
				errorString := fmt.Sprintf("error generating manifest: new release version %s is lower than existing release version %s", t.newVersion, t.oldVersion)

				Context(fmt.Sprintf("when the old version (of the release that provides redis-server) is %s and the new version is %s", t.oldVersion, t.newVersion), func() {
					BeforeEach(func() {
						serviceReleases[0].Version = t.newVersion

						oldManifest.Releases[0].Version = t.oldVersion
					})

					if t.returnsError {
						It("returns an error", func() {
							Expect(generateErr).To(MatchError(
								errorString,
							))
						})
					} else {
						It("returns no error", func() {
							Expect(generateErr).NotTo(HaveOccurred())
						})
					}
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

			Context("when the new release version (of the release that provides redis-server) cannot be parsed", func() {
				BeforeEach(func() {
					serviceReleases[0].Version = "oi"
				})

				It("returns an error", func() {
					Expect(generateErr).To(MatchError("oi is not a valid BOSH release version"))
				})
			})

			Context("when the old release version (of the release that provides redis-server) cannot be parsed", func() {
				BeforeEach(func() {
					oldManifest.Releases[0].Version = "oi"
				})

				It("returns an error", func() {
					Expect(generateErr).To(MatchError("oi is not a valid BOSH release version"))
				})
			})

			Context("when the old manifest does not contain any releases with the same name as the configured release that provides redis-server job", func() {
				BeforeEach(func() {
					oldManifest.Releases[0].Name = "i-dont-exist-in-newer-config"
				})

				It("returns an error", func() {
					Expect(generateErr).To(MatchError("no release with name some-release-name found in previous manifest"))
				})
			})

			Context("when the old manifest is valid", func() {
				BeforeEach(func() {
					oldManifest.Releases[0].Version = "1"
				})

				It("generates a new manifest with old values derived from arbitrary parameters", func() {
					Expect(generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["maxclients"]).To(Equal(47))
				})

				It("generates a new redis manifest", func() {
					Expect(generateErr).NotTo(HaveOccurred())

					Expect(generated.Name).To(Equal("some-instance-id"))
					Expect(generated.Releases).To(ConsistOf(
						bosh.Release{Name: "some-release-name", Version: "4"},
					))
					Expect(generated.Stemcells).To(HaveLen(1))
					Expect(generated.Stemcells[0].OS).To(Equal("some-stemcell-os"))
					Expect(generated.Stemcells[0].Version).To(Equal("1234"))

					Expect(generated.InstanceGroups).To(HaveLen(2))
					Expect(generated.InstanceGroups[0].Name).To(Equal("redis-server"))
					Expect(generated.InstanceGroups[0].Instances).To(Equal(45))

					Expect(generated.InstanceGroups[0].Jobs).To(ConsistOf(
						bosh.Job{Name: "redis-server", Release: "some-release-name"},
					))

					Expect(generated.InstanceGroups[0].VMType).To(Equal("dedicated-vm"))
					Expect(generated.InstanceGroups[0].PersistentDiskType).To(Equal("dedicated-disk"))
					Expect(generated.InstanceGroups[0].Networks).To(ConsistOf(bosh.Network{Name: "dedicated-network"}))
					Expect(generated.InstanceGroups[0].AZs).To(ConsistOf("dedicated-az1", "dedicated-az2"))
					Expect(generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["persistence"]).To(Equal("yes"))
					Expect(generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["password"]).To(Equal("some-password"))
				})

				Context("when arbitrary parameters are present that clash with values in the old manifest", func() {
					BeforeEach(func() {
						requestParams = map[string]interface{}{
							"parameters": map[string]interface{}{
								"maxclients": 56.0, // From JSON. No integers.
							},
						}
					})

					It("overrides the old manifest values with the new arbitrary parameters", func() {
						Expect(generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["password"]).To(Equal("some-password"))
						Expect(generated.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})["maxclients"]).To(Equal(56))
					})
				})
			})
		})

		Context("when a plan does not have an instance group named redis-server", func() {
			BeforeEach(func() {
				plan = serviceadapter.Plan{
					InstanceGroups: []serviceadapter.InstanceGroup{{Name: "not-redis-server"}},
				}
			})

			It("returns an error with a message for the cli user", func() {
				Expect(generateErr).To(HaveOccurred())
				Expect(generateErr).To(MatchError(ContainSubstring("Contact your operator, service configuration issue occurred")))
			})

			It("outputs a message for the operator to stderr", func() {
				Expect(stderr).To(gbytes.Say("no redis-server instance group definition found"))
			})
		})

		Describe("missing plan properties", func() {
			var ItRequiresTheProperty = func(property string) {
				Context(fmt.Sprintf("when a plan does not define the '%s' plan property", property), func() {
					BeforeEach(func() {
						propertySlice := strings.Split(property, ".")
						if len(propertySlice) == 1 {
							delete(dedicatedPlan.Properties, property)
						} else {
							delete(dedicatedPlan.Properties[propertySlice[0]].(map[string]interface{}), propertySlice[1])
						}
					})

					It("returns an error to the cli user", func() {
						Expect(generateErr).To(HaveOccurred())
						Expect(generateErr).To(MatchError(""))
					})

					It("logs an informative error for the operator", func() {
						Expect(stderr).To(gbytes.Say(fmt.Sprintf("the plan property '%s' is missing", property)))
					})
				})
			}

			ItRequiresTheProperty("persistence")
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
