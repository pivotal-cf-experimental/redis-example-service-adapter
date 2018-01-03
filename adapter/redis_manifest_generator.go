package adapter

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"io/ioutil"

	"github.com/pivotal-cf/on-demand-services-sdk/bosh"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"
	"gopkg.in/yaml.v2"
)

const (
	RedisServerPersistencePropertyKey = "persistence"
	RedisServerPort                   = 6379
	RedisJobName                      = "redis-server"
	HealthCheckErrandName             = "health-check"
	CleanupDataErrandName             = "cleanup-data"
	LifecycleErrandType               = "errand"
)

type generatorConfig struct {
	RedisInstanceGroupName string `yaml:"redis_instance_group_name"`
}

var CurrentPasswordGenerator = randomPasswordGenerator

type ManifestGenerator struct {
	StderrLogger           *log.Logger
	ConfigPath             string
	RedisInstanceGroupName string
}

func (m ManifestGenerator) GenerateManifest(
	serviceDeployment serviceadapter.ServiceDeployment,
	plan serviceadapter.Plan,
	requestParams serviceadapter.RequestParameters,
	previousManifest *bosh.BoshManifest,
	previousPlan *serviceadapter.Plan,
) (bosh.BoshManifest, error) {

	arbitraryParameters := requestParams.ArbitraryParams()
	illegalArbParams := findIllegalArbitraryParams(arbitraryParameters)
	if len(illegalArbParams) != 0 {
		return bosh.BoshManifest{}, fmt.Errorf("unsupported parameter(s) for this service plan: %s", strings.Join(illegalArbParams, ", "))
	}

	if previousManifest != nil {
		if err := m.validUpgradePath(*previousManifest, serviceDeployment.Releases); err != nil {
			return bosh.BoshManifest{}, err
		}
	}

	stemcellAlias := "only-stemcell"

	var err error
	m.RedisInstanceGroupName, err = m.getRedisInstanceGroupNameFromConfig()
	if err != nil {
		return bosh.BoshManifest{}, err
	}

	redisServerInstanceGroup := m.findRedisServerInstanceGroup(plan)
	if redisServerInstanceGroup == nil {
		m.StderrLogger.Println(fmt.Sprintf("no %s instance group definition found", m.RedisInstanceGroupName))
		return bosh.BoshManifest{}, errors.New("Contact your operator, service configuration issue occurred")
	}

	redisServerNetworks := mapNetworksToBoshNetworks(redisServerInstanceGroup.Networks)

	redisProperties, err := m.redisServerProperties(
		serviceDeployment.DeploymentName,
		plan.Properties,
		arbitraryParameters,
		previousManifest,
	)
	if err != nil {
		return bosh.BoshManifest{}, err
	}

	releases := []bosh.Release{}
	for _, release := range serviceDeployment.Releases {
		releases = append(releases, bosh.Release{
			Name:    release.Name,
			Version: release.Version,
		})
	}

	redisServerJob, err := m.gatherRedisServerJob(serviceDeployment.Releases)
	if err != nil {
		return bosh.BoshManifest{}, err
	}

	redisServerInstanceJobs := []bosh.Job{redisServerJob}

	if value, ok := plan.Properties["colocated_errand"].(bool); ok && value {
		for _, errandName := range []string{plan.LifecycleErrands.PostDeploy.Name, plan.LifecycleErrands.PreDelete.Name} {
			if errandName == "" {
				continue
			}

			job, err := gatherJob(serviceDeployment.Releases, errandName)
			if err != nil {
				return bosh.BoshManifest{}, err
			}

			redisServerInstanceJobs = append(redisServerInstanceJobs, job)
		}
	}

	migrations := []bosh.Migration{}
	for _, m := range redisServerInstanceGroup.MigratedFrom {
		migrations = append(migrations, bosh.Migration{
			Name: m.Name,
		})
	}

	newRedisInstanceGroup := bosh.InstanceGroup{
		Name:               redisServerInstanceGroup.Name,
		Instances:          redisServerInstanceGroup.Instances,
		Jobs:               redisServerInstanceJobs,
		VMType:             redisServerInstanceGroup.VMType,
		VMExtensions:       redisServerInstanceGroup.VMExtensions,
		PersistentDiskType: redisServerInstanceGroup.PersistentDiskType,
		Stemcell:           stemcellAlias,
		Networks:           redisServerNetworks,
		AZs:                redisServerInstanceGroup.AZs,
		Properties:         redisProperties,
		MigratedFrom:       migrations,
	}

	instanceGroups := []bosh.InstanceGroup{newRedisInstanceGroup}

	healthCheckInstanceGroup := findHealthCheckInstanceGroup(plan)

	if healthCheckInstanceGroup != nil {
		healthCheckProperties := m.healthCheckProperties(plan.Properties)

		healthCheckJob, err := gatherHealthCheckJob(serviceDeployment.Releases)

		if err != nil {
			return bosh.BoshManifest{}, err
		}

		healthCheckJobs := []bosh.Job{healthCheckJob}
		healthCheckNetworks := mapNetworksToBoshNetworks(healthCheckInstanceGroup.Networks)

		instanceGroups = append(instanceGroups, bosh.InstanceGroup{
			Name:               HealthCheckErrandName,
			Instances:          healthCheckInstanceGroup.Instances,
			Jobs:               healthCheckJobs,
			VMType:             healthCheckInstanceGroup.VMType,
			VMExtensions:       healthCheckInstanceGroup.VMExtensions,
			PersistentDiskType: healthCheckInstanceGroup.PersistentDiskType,
			Stemcell:           stemcellAlias,
			Networks:           healthCheckNetworks,
			AZs:                healthCheckInstanceGroup.AZs,
			Lifecycle:          LifecycleErrandType,
			Properties:         healthCheckProperties,
		})
	}

	cleanupDataInstanceGroup := findCleanupDataInstanceGroup(plan)

	if cleanupDataInstanceGroup != nil {
		cleanupDataProperties := m.cleanupDataProperties(plan.Properties)

		cleanupDataJob, err := gatherCleanupDataJob(serviceDeployment.Releases)
		if err != nil {
			return bosh.BoshManifest{}, err
		}

		cleanupDataJobs := []bosh.Job{cleanupDataJob}

		cleanupDataNetworks := mapNetworksToBoshNetworks(cleanupDataInstanceGroup.Networks)

		instanceGroups = append(instanceGroups, bosh.InstanceGroup{
			Name:               CleanupDataErrandName,
			Instances:          cleanupDataInstanceGroup.Instances,
			Jobs:               cleanupDataJobs,
			VMType:             cleanupDataInstanceGroup.VMType,
			VMExtensions:       cleanupDataInstanceGroup.VMExtensions,
			PersistentDiskType: cleanupDataInstanceGroup.PersistentDiskType,
			Stemcell:           stemcellAlias,
			Networks:           cleanupDataNetworks,
			AZs:                cleanupDataInstanceGroup.AZs,
			Lifecycle:          LifecycleErrandType,
			Properties:         cleanupDataProperties,
		})
	}

	return bosh.BoshManifest{
		Name:     serviceDeployment.DeploymentName,
		Releases: releases,
		Stemcells: []bosh.Stemcell{
			{
				Alias:   stemcellAlias,
				OS:      serviceDeployment.Stemcell.OS,
				Version: serviceDeployment.Stemcell.Version,
			},
		},
		InstanceGroups: instanceGroups,
		Update:         generateUpdateBlock(plan.Update, previousManifest),
		Properties:     map[string]interface{}{},
		Tags: map[string]interface{}{
			"product": "redis",
		},
	}, nil
}

func (m *ManifestGenerator) getRedisInstanceGroupNameFromConfig() (string, error) {
	generatorConfig := generatorConfig{}

	ymlFile, err := ioutil.ReadFile(m.ConfigPath)
	if err != nil {
		m.StderrLogger.Println(fmt.Sprintf("Error reading config file from %s", m.ConfigPath))
		return "", errors.New(fmt.Sprintf("Error reading config file from %s", m.ConfigPath))
	}

	err = yaml.Unmarshal(ymlFile, &generatorConfig)
	if err != nil {
		m.StderrLogger.Println("Error unmarshalling config")
		return "", errors.New("Error unmarshalling config")
	}

	return generatorConfig.RedisInstanceGroupName, nil
}

func findIllegalArbitraryParams(arbitraryParams map[string]interface{}) []string {
	var illegalParams []string
	for k, _ := range arbitraryParams {
		if k != "maxclients" {
			illegalParams = append(illegalParams, k)
		}
	}
	return illegalParams
}

func mapNetworksToBoshNetworks(networks []string) []bosh.Network {
	boshNetworks := []bosh.Network{}
	for _, network := range networks {
		boshNetworks = append(boshNetworks, bosh.Network{Name: network})
	}
	return boshNetworks
}

func randomPasswordGenerator() (string, error) {
	length := 20
	randomBytes := make([]byte, length)
	_, err := rand.Read(randomBytes)
	if err != nil {
		log.Printf("Error generating random bytes, %v", err)
		return "", err
	}
	randomStringBytes := make([]byte, base64.StdEncoding.EncodedLen(len(randomBytes)))
	base64.StdEncoding.Encode(randomStringBytes, randomBytes)
	return string(randomStringBytes), nil
}

func findInstanceGroup(plan serviceadapter.Plan, instanceGroupName string) *serviceadapter.InstanceGroup {
	for _, instanceGroup := range plan.InstanceGroups {
		if instanceGroup.Name == instanceGroupName {
			return &instanceGroup
		}
	}

	return nil
}

func (m *ManifestGenerator) findRedisServerInstanceGroup(plan serviceadapter.Plan) *serviceadapter.InstanceGroup {
	return findInstanceGroup(plan, m.RedisInstanceGroupName)
}

func findHealthCheckInstanceGroup(plan serviceadapter.Plan) *serviceadapter.InstanceGroup {
	return findInstanceGroup(plan, HealthCheckErrandName)
}

func findCleanupDataInstanceGroup(plan serviceadapter.Plan) *serviceadapter.InstanceGroup {
	return findInstanceGroup(plan, CleanupDataErrandName)
}

var versionRegexp = regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\+dev\.(\d+))?`)

func parseReleaseVersion(versionString string) (int, int, int, error) {
	submatches := versionRegexp.FindStringSubmatch(versionString)

	if len(submatches) == 0 {
		return 0, 0, 0, fmt.Errorf("%s is not a valid BOSH release version", versionString)
	}

	major, err := strconv.Atoi(submatches[1])
	if err != nil {
		return 0, 0, 0, err
	}

	minor := 0
	if submatches[2] != "" {
		minor, err = strconv.Atoi(submatches[2])
		if err != nil {
			return 0, 0, 0, err
		}
	}

	patch := 0
	if submatches[3] != "" {
		patch, err = strconv.Atoi(submatches[3])
		if err != nil {
			return 0, 0, 0, err
		}
	}

	return major, minor, patch, nil
}

func generateUpdateBlock(update *serviceadapter.Update, previousManifest *bosh.BoshManifest) bosh.Update {
	if update != nil {
		return bosh.Update{
			Canaries:        update.Canaries,
			MaxInFlight:     update.MaxInFlight,
			CanaryWatchTime: update.CanaryWatchTime,
			UpdateWatchTime: update.UpdateWatchTime,
			Serial:          update.Serial,
		}
	} else {
		updateBlock := bosh.Update{
			Canaries:        4,
			CanaryWatchTime: "30000-240000",
			UpdateWatchTime: "30000-240000",
			MaxInFlight:     4,
		}

		if previousManifest == nil {
			return updateBlock
		}

		updateBlock.Canaries = 1
		updateBlock.MaxInFlight = 1
		return updateBlock
	}
}

func gatherJob(releases serviceadapter.ServiceReleases, jobName string) (bosh.Job, error) {
	release, err := findReleaseForJob(jobName, releases)
	if err != nil {
		return bosh.Job{}, err
	}
	return bosh.Job{Name: jobName, Release: release.Name}, nil
}

func (m *ManifestGenerator) gatherRedisServerJob(releases serviceadapter.ServiceReleases) (bosh.Job, error) {
	return gatherJob(releases, RedisJobName)
}

func gatherHealthCheckJob(releases serviceadapter.ServiceReleases) (bosh.Job, error) {
	return gatherJob(releases, HealthCheckErrandName)
}

func gatherCleanupDataJob(releases serviceadapter.ServiceReleases) (bosh.Job, error) {
	return gatherJob(releases, CleanupDataErrandName)
}

func findReleaseForJob(requiredJob string, releases serviceadapter.ServiceReleases) (serviceadapter.ServiceRelease, error) {
	releasesThatProvideRequiredJob := serviceadapter.ServiceReleases{}

	for _, release := range releases {
		for _, providedJob := range release.Jobs {
			if providedJob == requiredJob {
				releasesThatProvideRequiredJob = append(releasesThatProvideRequiredJob, release)
			}
		}
	}

	if len(releasesThatProvideRequiredJob) == 0 {
		return serviceadapter.ServiceRelease{}, fmt.Errorf("no release provided for job %s", requiredJob)
	}

	if len(releasesThatProvideRequiredJob) > 1 {
		releaseNames := []string{}
		for _, release := range releasesThatProvideRequiredJob {
			releaseNames = append(releaseNames, release.Name)
		}

		return serviceadapter.ServiceRelease{}, fmt.Errorf("job %s defined in multiple releases: %s", requiredJob, strings.Join(releaseNames, ", "))
	}

	return releasesThatProvideRequiredJob[0], nil
}

func redisPlanProperties(manifest bosh.BoshManifest) map[interface{}]interface{} {
	return manifest.InstanceGroups[0].Properties["redis"].(map[interface{}]interface{})
}

func (m ManifestGenerator) redisServerProperties(deploymentName string, planProperties serviceadapter.Properties, arbitraryParams map[string]interface{}, previousManifest *bosh.BoshManifest) (map[string]interface{}, error) {
	var previousRedisProperties map[interface{}]interface{}
	if previousManifest != nil {
		previousRedisProperties = redisPlanProperties(*previousManifest)
	}

	persistence, err := m.persistenceForRedisServer(planProperties)
	if err != nil {
		return nil, err
	}

	password, err := passwordForRedisServer(previousRedisProperties)
	if err != nil {
		return nil, err
	}

	maxClients := maxClientsForRedisServer(arbitraryParams, previousRedisProperties)

	return map[string]interface{}{
		"redis": map[interface{}]interface{}{
			"persistence": persistence,
			"password":    password,
			"maxclients":  maxClients,
		},
	}, nil
}

func passwordForRedisServer(previousManifestProperties map[interface{}]interface{}) (string, error) {
	if previousManifestProperties != nil {
		return previousManifestProperties["password"].(string), nil
	}

	return CurrentPasswordGenerator()
}

func maxClientsForRedisServer(arbitraryParams map[string]interface{}, previousManifestProperties map[interface{}]interface{}) int {
	if configuredMax, ok := arbitraryParams["maxclients"]; ok {
		return int(configuredMax.(float64))
	} else if previousManifestProperties != nil {
		return previousManifestProperties["maxclients"].(int)
	}
	return 10000
}

func (m *ManifestGenerator) persistenceForRedisServer(planProperties serviceadapter.Properties) (string, error) {
	persistenceConfig, found := planProperties[RedisServerPersistencePropertyKey]
	if !found {
		m.StderrLogger.Println(fmt.Sprintf("the plan property '%s' is missing", RedisServerPersistencePropertyKey))
		return "", errors.New("")
	}
	persistence := "no"
	if persistenceConfig.(bool) {
		persistence = "yes"
	}
	return persistence, nil
}

func (m *ManifestGenerator) healthCheckProperties(
	planProperties serviceadapter.Properties,
) map[string]interface{} {
	return errandProperties(HealthCheckErrandName, planProperties)
}

func (m *ManifestGenerator) cleanupDataProperties(
	planProperties serviceadapter.Properties,
) map[string]interface{} {
	return errandProperties(CleanupDataErrandName, planProperties)
}

func errandProperties(
	errandName string,
	planProperties serviceadapter.Properties,
) map[string]interface{} {
	if planProperties["systest_errand_failure_override"] == true {
		return map[string]interface{}{
			errandName: map[interface{}]interface{}{
				"systest-failure-override": true,
			},
		}
	}

	return nil
}

func oldGreaterThanNew(oldMajorVersion, oldMinorVersion, oldPatchVersion, newMajorVersion, newMinorVersion, newPatchVersion int) bool {
	if oldMajorVersion != newMajorVersion {
		return oldMajorVersion > newMajorVersion
	}
	if oldMinorVersion != newMinorVersion {
		return oldMinorVersion > newMinorVersion
	}
	return oldPatchVersion > newPatchVersion
}

func findOldManifestRedisRelease(redisReleaseName string, previousManifestReleases []bosh.Release) (bosh.Release, error) {
	for _, oldManifestRelease := range previousManifestReleases {
		if oldManifestRelease.Name == redisReleaseName {
			return oldManifestRelease, nil
		}
	}

	return bosh.Release{}, fmt.Errorf("no release with name %s found in previous manifest", redisReleaseName)
}

func (m *ManifestGenerator) validUpgradePath(previousManifest bosh.BoshManifest, serviceReleases serviceadapter.ServiceReleases) error {
	newRedisRelease, err := findReleaseForJob(RedisJobName, serviceReleases)
	if err != nil {
		return err
	}

	oldRedisRelease, err := findOldManifestRedisRelease(newRedisRelease.Name, previousManifest.Releases)
	if err != nil {
		return err
	}

	// Allow upgrade to/from latest
	if newRedisRelease.Version == "latest" || oldRedisRelease.Version == "latest" {
		return nil
	}

	newMajorVersion, newMinorVersion, newPatchVersion, err := parseReleaseVersion(newRedisRelease.Version)
	if err != nil {
		return err
	}

	oldMajorVersion, oldMinorVersion, oldPatchVersion, err := parseReleaseVersion(oldRedisRelease.Version)
	if err != nil {
		return err
	}

	if oldGreaterThanNew(oldMajorVersion, oldMinorVersion, oldPatchVersion, newMajorVersion, newMinorVersion, newPatchVersion) {
		return fmt.Errorf(
			"error generating manifest: new release version %s is lower than existing release version %s",
			newRedisRelease.Version,
			oldRedisRelease.Version,
		)
	}

	return nil
}
