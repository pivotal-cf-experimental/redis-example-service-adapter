package adapter_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestRedismanifest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Redis Service Adapter Suite")
}

func getFixturePath(filename string) string {
	cwd, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	return filepath.Join(cwd, "fixtures", filename)
}
