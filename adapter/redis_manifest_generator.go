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

	"github.com/pborman/uuid"
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

var CurrentPasswordGenerator = randomPasswordGenerator

const (
	ManagedSecretValue          = "HardcodedAdapterValue"
	ManagedSecretKey            = "odb_managed_secret"
	IgnoreSecretKey             = "ignore_secret"
	GeneratedSecretKey          = "generated_secret"
	GeneratedSecretVariableName = "secret_pass"
	CertificateVariableName     = "instance_certificate"
	CloudConfigKey              = "cloud"
	VMExtensionsConfigKey       = "vm_extensions_config"
)

type ManifestGenerator struct {
	StderrLogger *log.Logger
	Config       Config
}

func (m ManifestGenerator) GenerateManifest(params serviceadapter.GenerateManifestParams) (serviceadapter.GenerateManifestOutput, error) {

	ctx := params.RequestParams.ArbitraryContext()
	platform := params.RequestParams.Platform()
	if len(ctx) == 0 || platform != "cloudfoundry" {
		m.StderrLogger.Println("Non Cloud Foundry platform (or pre OSBAPI 2.13) detected")
	}
	arbitraryParameters := params.RequestParams.ArbitraryParams()
	illegalArbParams := findIllegalArbitraryParams(arbitraryParameters)
	if len(illegalArbParams) != 0 {
		return serviceadapter.GenerateManifestOutput{}, fmt.Errorf("unsupported parameter(s) for this service plan: %s", strings.Join(illegalArbParams, ", "))
	}

	if params.PreviousManifest != nil {
		if err := m.validUpgradePath(*params.PreviousManifest, params.ServiceDeployment.Releases); err != nil {
			return serviceadapter.GenerateManifestOutput{}, err
		}
	}

	stemcellAlias := "only-stemcell"

	var err error
	managedSecretValue := ManagedSecretValue
	if requestParamsOdbManagedSecret, found := params.RequestParams.ArbitraryParams()[ManagedSecretKey]; found {
		managedSecretValue = requestParamsOdbManagedSecret.(string)
		m.Config.IgnoreODBManagedSecretOnUpdate = true
	}

	vmExtensionsConfig := ""
	if requestParamsVMExtensionsConfig, found := params.RequestParams.ArbitraryParams()[VMExtensionsConfigKey]; found {
		vmExtensionsConfig = requestParamsVMExtensionsConfig.(string)
	}

	redisServerInstanceGroup := m.findRedisServerInstanceGroup(params.Plan)
	if redisServerInstanceGroup == nil {
		m.StderrLogger.Println(fmt.Sprintf("no %s instance group definition found", m.Config.RedisInstanceGroupName))
		return serviceadapter.GenerateManifestOutput{}, errors.New("Contact your operator, service configuration issue occurred")
	}

	newSecrets := serviceadapter.ODBManagedSecrets{}

	redisServerNetworks := mapNetworksToBoshNetworks(redisServerInstanceGroup.Networks)

	redisProperties, err := m.redisServerProperties(
		params.ServiceDeployment.DeploymentName,
		params.Plan.Properties,
		arbitraryParameters,
		params.PreviousManifest,
		newSecrets,
		params.PreviousSecrets,
	)
	if err != nil {
		return serviceadapter.GenerateManifestOutput{}, err
	}

	redisServerJob, err := m.gatherRedisServerJob(params.ServiceDeployment.Releases)
	if err != nil {
		return serviceadapter.GenerateManifestOutput{}, err
	}
	redisServerJob.Properties = redisProperties

	redisServerInstanceJobs := []bosh.Job{redisServerJob}

	if value, ok := params.Plan.Properties["colocated_errand"].(bool); ok && value {
		var errands []serviceadapter.Errand
		errands = append(params.Plan.LifecycleErrands.PreDelete, params.Plan.LifecycleErrands.PostDeploy...)

		for _, errand := range errands {
			if len(errand.Instances) == 0 {
				continue
			}
			job, err := gatherJob(params.ServiceDeployment.Releases, errand.Name)
			if err != nil {
				return serviceadapter.GenerateManifestOutput{}, err
			}

			redisServerInstanceJobs = append(redisServerInstanceJobs, job)
		}
	}

	var migrations []bosh.Migration
	for _, m := range redisServerInstanceGroup.MigratedFrom {
		migrations = append(migrations, bosh.Migration{
			Name: m.Name,
		})
	}

	redisServerVMExtensions, err := m.gatherRedisServerVMExtensions(
		redisServerInstanceGroup.VMExtensions,
		vmExtensionsConfig,
		params.PreviousManifest,
	)
	if err != nil {
		return serviceadapter.GenerateManifestOutput{}, err
	}

	newRedisInstanceGroup := bosh.InstanceGroup{
		Name:               redisServerInstanceGroup.Name,
		Instances:          redisServerInstanceGroup.Instances,
		Jobs:               redisServerInstanceJobs,
		VMType:             redisServerInstanceGroup.VMType,
		VMExtensions:       redisServerVMExtensions,
		PersistentDiskType: redisServerInstanceGroup.PersistentDiskType,
		Stemcell:           stemcellAlias,
		Networks:           redisServerNetworks,
		AZs:                redisServerInstanceGroup.AZs,
		MigratedFrom:       migrations,
	}

	instanceGroups := []bosh.InstanceGroup{newRedisInstanceGroup}

	healthCheckInstanceGroup := findHealthCheckInstanceGroup(params.Plan)

	if healthCheckInstanceGroup != nil {
		healthCheckProperties := m.healthCheckProperties(params.Plan.Properties)

		healthCheckJob, err := gatherHealthCheckJob(params.ServiceDeployment.Releases)

		if err != nil {
			return serviceadapter.GenerateManifestOutput{}, err
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

	cleanupDataInstanceGroup := findCleanupDataInstanceGroup(params.Plan)

	if cleanupDataInstanceGroup != nil {
		cleanupDataProperties := m.cleanupDataProperties(params.Plan.Properties)

		cleanupDataJob, err := gatherCleanupDataJob(params.ServiceDeployment.Releases)
		if err != nil {
			return serviceadapter.GenerateManifestOutput{}, err
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

	releases := []bosh.Release{}
	for _, release := range params.ServiceDeployment.Releases {
		releases = append(releases, bosh.Release{
			Name:    release.Name,
			Version: release.Version,
		})
	}

	newManifest := bosh.BoshManifest{
		Name:     params.ServiceDeployment.DeploymentName,
		Releases: releases,
		Stemcells: []bosh.Stemcell{
			{
				Alias:   stemcellAlias,
				OS:      params.ServiceDeployment.Stemcells[0].OS,
				Version: params.ServiceDeployment.Stemcells[0].Version,
			},
		},
		InstanceGroups: instanceGroups,
		Update:         generateUpdateBlock(params.Plan.Update, params.PreviousManifest),
		Properties:     map[string]interface{}{},
		Tags: map[string]interface{}{
			"product": "redis",
		},
		Variables: []bosh.Variable{
			{Name: GeneratedSecretVariableName, Type: "password"},
			{
				Name:       CertificateVariableName,
				Type:       "certificate",
				UpdateMode: "no-overwrite",
				Options:    map[string]interface{}{"is_ca": true, "common_name": "redis"},
				Consumes: &bosh.VariableConsumes{
					AlternativeName: bosh.VariableConsumesLink{
						From:       "redis-server-link",
						Properties: map[string]interface{}{"wildcard": true},
					},
					CommonName: bosh.VariableConsumesLink{
						From: "redis-server-link",
					},
				},
			},
		},
	}
	if useShortDNSAddress, set := params.Plan.Properties["use_short_dns_addresses"]; set {
		newManifest.Features.UseShortDNSAddresses = bosh.BoolPointer(useShortDNSAddress == true)
	}
	if somethingCompletelyDifferent, set := params.Plan.Properties["something_completely_different"]; set {
		newManifest.Features.ExtraFeatures = map[string]interface{}{
			"something_completely_different": somethingCompletelyDifferent,
		}
	}
	newSecrets[ManagedSecretKey] = managedSecretValue

	newConfigs := serviceadapter.BOSHConfigs{}
	if vmExtensionsConfig != "" {
		newConfigs[CloudConfigKey] = vmExtensionsConfig
	}

	return serviceadapter.GenerateManifestOutput{
		Manifest:          newManifest,
		ODBManagedSecrets: newSecrets,
		Configs:           newConfigs,
	}, nil
}

func findIllegalArbitraryParams(arbitraryParams map[string]interface{}) []string {
	var illegalParams []string
	for k, _ := range arbitraryParams {
		if k == "maxclients" || k == "credhub_secret_path" || k == ManagedSecretKey || k == VMExtensionsConfigKey {
			continue
		}
		illegalParams = append(illegalParams, k)
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
	return findInstanceGroup(plan, m.Config.RedisInstanceGroupName)
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

func generateUpdateBlock(update *serviceadapter.Update, previousManifest *bosh.BoshManifest) *bosh.Update {
	if update != nil {
		return &bosh.Update{
			Canaries:        update.Canaries,
			MaxInFlight:     update.MaxInFlight,
			CanaryWatchTime: update.CanaryWatchTime,
			UpdateWatchTime: update.UpdateWatchTime,
			Serial:          update.Serial,
		}
	} else {
		updateBlock := &bosh.Update{
			Canaries:        4,
			CanaryWatchTime: "30000-240000",
			UpdateWatchTime: "30000-240000",
			MaxInFlight:     4,
			VmStrategy:      "delete-create",
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
	redisServerJob, err := gatherJob(releases, RedisJobName)
	if err != nil {
		return bosh.Job{}, errors.New(fmt.Sprintf("error gathering redis server job: %s", err))
	}
	redisServerJob = redisServerJob.AddCustomProviderDefinition("redis-server-link", "address", nil)
	return redisServerJob.AddSharedProvidesLink("redis"), nil
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
	return manifest.InstanceGroups[0].Jobs[0].Properties["redis"].(map[interface{}]interface{})
}

func (m ManifestGenerator) redisServerProperties(
	deploymentName string,
	planProperties serviceadapter.Properties,
	arbitraryParams map[string]interface{},
	previousManifest *bosh.BoshManifest,
	newSecrets serviceadapter.ODBManagedSecrets,
	previousSecrets serviceadapter.ManifestSecrets) (map[string]interface{}, error) {
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

	managedSecretKey := managedSecretKeyForRedisServer(previousRedisProperties, m.Config.IgnoreODBManagedSecretOnUpdate)

	maxClients := maxClientsForRedisServer(arbitraryParams, previousRedisProperties)

	properties := map[interface{}]interface{}{
		"persistence":      persistence,
		"password":         password,
		"maxclients":       maxClients,
		GeneratedSecretKey: "((" + GeneratedSecretVariableName + "))",
		ManagedSecretKey:   managedSecretKey,
		"ca_cert":          "((" + CertificateVariableName + ".ca))",
		"certificate":      "((" + CertificateVariableName + ".certificate))",
		"private_key":      "((" + CertificateVariableName + ".private_key))",
	}

	if secretFromPlan, exists := planProperties["plan_secret"]; exists && m.Config.SecureManifestsEnabled {
		secretKey := "plan_secret_key" + uuid.New()[:6]
		newSecrets[secretKey] = secretFromPlan
		planSecret := fmt.Sprintf("((%s:%s))", serviceadapter.ODBSecretPrefix, secretKey)
		if previousSecrets != nil {
			existingCredhubPath, ok := previousManifest.InstanceGroups[0].Jobs[0].Properties["redis"].(map[interface{}]interface{})["plan_secret"]
			if ok && previousSecrets[existingCredhubPath.(string)] == secretFromPlan {
				planSecret = existingCredhubPath.(string)
				delete(newSecrets, secretKey)
			}
		}
		properties["plan_secret"] = planSecret
	}

	if secretPath, ok := arbitraryParams["credhub_secret_path"]; ok {
		properties["secret"] = "((" + secretPath.(string) + "))"
	} else if secret, ok := previousRedisProperties["secret"]; ok {
		properties["secret"] = secret
	}

	return map[string]interface{}{
		"redis": properties,
	}, nil
}

func managedSecretKeyForRedisServer(previousManifestProperties map[interface{}]interface{}, ignoreODBSecret bool) string {
	if previousManifestProperties != nil {
		managedSecretKey, managedSecretFound := previousManifestProperties[ManagedSecretKey].(string)
		if managedSecretFound && !ignoreODBSecret {
			return managedSecretKey
		}
	}

	return "((" + serviceadapter.ODBSecretPrefix + ":" + ManagedSecretKey + "))"
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
	if planProperties["systest_errand_failure_override"] == errandName {
		return map[string]interface{}{
			errandName: map[interface{}]interface{}{
				"systest-failure-override": true,
			},
		}
	}

	if planProperties["systest_errand_sleep"] != nil {
		return map[string]interface{}{
			errandName: map[interface{}]interface{}{
				"systest-sleep": planProperties["systest_errand_sleep"],
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

func findInstanceGroupFromPreviousManifest(previousManifest bosh.BoshManifest, instanceGroupName string) *bosh.InstanceGroup {
	for _, instanceGroup := range previousManifest.InstanceGroups {
		if instanceGroup.Name == instanceGroupName {
			return &instanceGroup
		}
	}

	return nil
}

func (m *ManifestGenerator) findRedisServerInstanceGroupFromPreviousManifest(previousManifest bosh.BoshManifest) *bosh.InstanceGroup {
	return findInstanceGroupFromPreviousManifest(previousManifest, m.Config.RedisInstanceGroupName)
}

func (m *ManifestGenerator) gatherRedisServerVMExtensions(
	vmExtensions serviceadapter.VMExtensions,
	vmExtensionsConfig string,
	previousManifest *bosh.BoshManifest,
) (serviceadapter.VMExtensions, error) {
	if vmExtensionsConfig != "" {
		vmExtensionNames, err := parseVMExtensionsConfig(vmExtensionsConfig)
		if err != nil {
			return vmExtensions, err
		}
		vmExtensions = append(vmExtensions, vmExtensionNames...)
	} else if previousManifest != nil {
		redisServerInstanceGroup := m.findRedisServerInstanceGroupFromPreviousManifest(*previousManifest)
		if redisServerInstanceGroup != nil {
			vmExtensions = redisServerInstanceGroup.VMExtensions
		}
	}

	return vmExtensions, nil
}

type VMExtension struct {
	Name            string                 `yaml:"name"`
	CloudProperties map[string]interface{} `yaml:"cloud_properties"`
}

type CloudConfig struct {
	VMExtensions []VMExtension `yaml:"vm_extensions"`
}

func parseVMExtensionsConfig(vmExtensionsConfig string) (serviceadapter.VMExtensions, error) {
	var vmExtensionNames serviceadapter.VMExtensions
	cloudConfig := CloudConfig{}
	if err := yaml.Unmarshal([]byte(vmExtensionsConfig), &cloudConfig); err != nil {
		return vmExtensionNames, err
	}
	for _, vmExtension := range cloudConfig.VMExtensions {
		vmExtensionNames = append(vmExtensionNames, vmExtension.Name)
	}
	return vmExtensionNames, nil
}
