package testdata

import (
	"bytes"
	"embed"
	"path/filepath"

	kubeyaml "k8s.io/apimachinery/pkg/util/yaml"

	sscsiv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/secretsync/v1alpha1"
)

var (
	//go:embed secretsyncs/*
	secretSyncs embed.FS
	//go:embed secretproviderclasses/*
	secretProviderClasses embed.FS
	//go:embed apivalidation/*
	apiValidation embed.FS
)

func GetSecretSyncOrDie(name string) *secretsyncv1alpha1.SecretSync {
	return readOrDie[secretsyncv1alpha1.SecretSync](secretSyncs, filepath.Join("secretsyncs", name+".yaml"))
}

func GetE2ESecretProviderClassOrDie() *sscsiv1.SecretProviderClass {
	return readOrDie[sscsiv1.SecretProviderClass](secretProviderClasses, filepath.Join("secretproviderclasses", "e2e-providerspc.yaml"))
}

func GetAPIValidationTestsOrDie() []*secretsyncv1alpha1.SecretSync {
	files, err := apiValidation.ReadDir("apivalidation")
	if err != nil {
		panic(err)
	}

	ret := make([]*secretsyncv1alpha1.SecretSync, 0, len(files))
	for _, f := range files {
		ssObj := readOrDie[secretsyncv1alpha1.SecretSync](apiValidation, filepath.Join("apivalidation", f.Name()))
		ret = append(ret, ssObj)
	}
	return ret
}

func readOrDie[T any](fs embed.FS, path string) *T {
	cont, err := fs.ReadFile(path)
	if err != nil {
		panic(err)
	}

	ret, err := decodeFromBytes[T](cont)
	if err != nil {
		panic(err)
	}

	return ret
}

func decodeFromBytes[T any](data []byte) (*T, error) {
	var ret = new(T)

	src := bytes.NewReader(data)

	err := kubeyaml.NewYAMLOrJSONDecoder(src, src.Len()).Decode(ret)
	return ret, err
}
