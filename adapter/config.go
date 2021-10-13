package adapter

import (
	"io/ioutil"
	"log"

	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v3"
)

type Config struct {
	RedisInstanceGroupName         string `yaml:"redis_instance_group_name"`
	IgnoreODBManagedSecretOnUpdate bool   `yaml:"ignore_odb_managed_secret_on_update"`
	SecureManifestsEnabled         bool   `yaml:"secure_manifests_enabled"`
}

func LoadConfig(path string, logger *log.Logger) (Config, error) {
	config := Config{}

	ymlFile, err := ioutil.ReadFile(path)
	if err != nil {
		wrappedErr := errors.Wrap(err, "Error, could not read config file")
		logger.Println(wrappedErr.Error())
		return Config{}, wrappedErr
	}

	err = yaml.Unmarshal(ymlFile, &config)
	if err != nil {
		wrappedErr := errors.Wrap(err, "Error, could not parse config YAML")
		logger.Println(wrappedErr.Error())
		return Config{}, wrappedErr
	}
	return config, nil
}
