package tests

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	k8sRand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/lyft/flytestdlib/config"
	"github.com/lyft/flytestdlib/internal/utils"
	"github.com/spf13/pflag"

	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/assert"
)

type accessorCreatorFn func(registry config.Section, configPath string) config.Accessor

func getRandInt() uint64 {
	c := 10
	b := make([]byte, c)
	_, err := rand.Read(b)
	if err != nil {
		return 0
	}

	return binary.BigEndian.Uint64(b)
}

func tempFileName(pattern string) string {
	// TODO: Remove this hack after we use Go1.11 everywhere:
	// https://github.com/golang/go/commit/191efbc419d7e5dec842c20841f6f716da4b561d

	var prefix, suffix string
	if pos := strings.LastIndex(pattern, "*"); pos != -1 {
		prefix, suffix = pattern[:pos], pattern[pos+1:]
	} else {
		prefix = pattern
	}

	return filepath.Join(os.TempDir(), prefix+k8sRand.String(6)+suffix)
}

func populateConfigData(configPath string) (TestConfig, error) {
	expected := TestConfig{
		MyComponentConfig: MyComponentConfig{
			StringValue: fmt.Sprintf("Hello World %v", getRandInt()),
		},
		OtherComponentConfig: OtherComponentConfig{
			StringValue:   fmt.Sprintf("Hello World %v", getRandInt()),
			URLValue:      config.URL{URL: utils.MustParseURL("http://something.com")},
			DurationValue: config.Duration{Duration: time.Second * 20},
		},
	}

	raw, err := yaml.Marshal(expected)
	if err != nil {
		return TestConfig{}, err
	}

	return expected, ioutil.WriteFile(configPath, raw, os.ModePerm)
}

func TestGetEmptySection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		r := config.GetSection("Empty")
		assert.Nil(t, r)
	})
}

type ComplexType struct {
	IntValue int `json:"int-val"`
}

type ComplexTypeArray []ComplexType

type ConfigWithLists struct {
	ListOfStuff []ComplexType `json:"list"`
	StringValue string        `json:"string-val"`
}

type ConfigWithMaps struct {
	MapOfStuff     map[string]ComplexType `json:"m"`
	MapWithoutJSON map[string]ComplexType
}

type ConfigWithJSONTypes struct {
	Duration config.Duration `json:"duration"`
}

func TestAccessor_InitializePflags(t *testing.T) {
	for _, provider := range providers {
		t.Run(fmt.Sprintf("[%v] Unused flag", provider(config.Options{}).ID()), func(t *testing.T) {
			reg := config.NewRootSection()
			_, err := reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)
			v := provider(config.Options{
				SearchPaths: []string{filepath.Join("testdata", "config.yaml")},
				RootSection: reg,
			})

			set := pflag.NewFlagSet("test", pflag.ContinueOnError)
			set.String("flag1", "123", "")
			v.InitializePflags(set)
			assert.NoError(t, v.UpdateConfig(context.TODO()))
		})

		t.Run(fmt.Sprintf("[%v] Override string value", provider(config.Options{}).ID()), func(t *testing.T) {
			reg := config.NewRootSection()
			_, err := reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			v := provider(config.Options{
				SearchPaths: []string{filepath.Join("testdata", "config.yaml")},
				RootSection: reg,
			})

			set := pflag.NewFlagSet("test", pflag.ContinueOnError)
			v.InitializePflags(set)
			key := "MY_COMPONENT.STR2"
			assert.NoError(t, os.Setenv(key, "123"))
			defer func() { assert.NoError(t, os.Unsetenv(key)) }()
			assert.NoError(t, v.UpdateConfig(context.TODO()))
			r := reg.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			assert.Equal(t, "123", r.StringValue2)
		})

		t.Run(fmt.Sprintf("[%v] Parse from config file", provider(config.Options{}).ID()), func(t *testing.T) {
			reg := config.NewRootSection()
			_, err := reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			_, err = reg.RegisterSection(OtherComponentSectionKey, &OtherComponentConfig{})
			assert.NoError(t, err)

			v := provider(config.Options{
				SearchPaths: []string{filepath.Join("testdata", "config.yaml")},
				RootSection: reg,
			})

			set := pflag.NewFlagSet("test", pflag.ExitOnError)
			v.InitializePflags(set)
			assert.NoError(t, v.UpdateConfig(context.TODO()))
			r := reg.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			assert.Equal(t, "Hello World", r.StringValue)
			otherC := reg.GetSection(OtherComponentSectionKey).GetConfig().(*OtherComponentConfig)
			assert.Equal(t, 4, otherC.IntValue)
			assert.Equal(t, []string{"default value"}, otherC.StringArrayWithDefaults)
		})
	}
}

func TestStrictAccessor(t *testing.T) {
	for _, provider := range providers {
		t.Run(fmt.Sprintf("[%v] Bad config", provider(config.Options{}).ID()), func(t *testing.T) {
			reg := config.NewRootSection()
			v := provider(config.Options{
				StrictMode:  true,
				SearchPaths: []string{filepath.Join("testdata", "bad_config.yaml")},
				RootSection: reg,
			})

			set := pflag.NewFlagSet("test", pflag.ExitOnError)
			v.InitializePflags(set)
			assert.Error(t, v.UpdateConfig(context.TODO()))
		})

		t.Run(fmt.Sprintf("[%v] Set through env", provider(config.Options{}).ID()), func(t *testing.T) {
			reg := config.NewRootSection()
			_, err := reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			_, err = reg.RegisterSection(OtherComponentSectionKey, &OtherComponentConfig{})
			assert.NoError(t, err)

			v := provider(config.Options{
				StrictMode:  true,
				SearchPaths: []string{filepath.Join("testdata", "config.yaml")},
				RootSection: reg,
			})

			set := pflag.NewFlagSet("other-component.string-value", pflag.ExitOnError)
			v.InitializePflags(set)

			key := "OTHER_COMPONENT.STRING_VALUE"
			assert.NoError(t, os.Setenv(key, "set from env"))
			defer func() { assert.NoError(t, os.Unsetenv(key)) }()
			assert.NoError(t, v.UpdateConfig(context.TODO()))
		})
	}
}

func TestAccessor_UpdateConfig(t *testing.T) {
	for _, provider := range providers {
		t.Run(fmt.Sprintf("[%v] Static File", provider(config.Options{}).ID()), func(t *testing.T) {
			reg := config.NewRootSection()
			_, err := reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			v := provider(config.Options{
				SearchPaths: []string{filepath.Join("testdata", "config.yaml")},
				RootSection: reg,
			})

			assert.NoError(t, v.UpdateConfig(context.TODO()))
			r := reg.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			assert.Equal(t, "Hello World", r.StringValue)
		})

		t.Run(fmt.Sprintf("[%v] Nested", provider(config.Options{}).ID()), func(t *testing.T) {
			root := config.NewRootSection()
			section, err := root.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			_, err = section.RegisterSection("nested", &OtherComponentConfig{})
			assert.NoError(t, err)

			v := provider(config.Options{
				SearchPaths: []string{filepath.Join("testdata", "nested_config.yaml")},
				RootSection: root,
			})

			assert.NoError(t, v.UpdateConfig(context.TODO()))
			r := root.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			assert.Equal(t, "Hello World", r.StringValue)

			nested := section.GetSection("nested").GetConfig().(*OtherComponentConfig)
			assert.Equal(t, "Hey there!", nested.StringValue)
		})

		t.Run(fmt.Sprintf("[%v] Array Configs", provider(config.Options{}).ID()), func(t *testing.T) {
			root := config.NewRootSection()
			section, err := root.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			_, err = section.RegisterSection("nested", &ComplexTypeArray{})
			assert.NoError(t, err)

			_, err = root.RegisterSection("array-config", &ComplexTypeArray{})
			assert.NoError(t, err)

			v := provider(config.Options{
				SearchPaths: []string{filepath.Join("testdata", "array_configs.yaml")},
				RootSection: root,
			})

			assert.NoError(t, v.UpdateConfig(context.TODO()))
			r := root.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			assert.Equal(t, "Hello World", r.StringValue)

			nested := section.GetSection("nested").GetConfig().(*ComplexTypeArray)
			assert.Len(t, *nested, 1)
			assert.Equal(t, 1, (*nested)[0].IntValue)

			topLevel := root.GetSection("array-config").GetConfig().(*ComplexTypeArray)
			assert.Len(t, *topLevel, 2)
			assert.Equal(t, 4, (*topLevel)[1].IntValue)
		})

		t.Run(fmt.Sprintf("[%v] Override in Env Var", provider(config.Options{}).ID()), func(t *testing.T) {
			reg := config.NewRootSection()
			_, err := reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			v := provider(config.Options{
				SearchPaths: []string{filepath.Join("testdata", "config.yaml")},
				RootSection: reg,
			})
			key := strings.ToUpper("my-component.str")
			assert.NoError(t, os.Setenv(key, "Set From Env"))
			defer func() { assert.NoError(t, os.Unsetenv(key)) }()
			assert.NoError(t, v.UpdateConfig(context.TODO()))
			r := reg.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			assert.Equal(t, "Set From Env", r.StringValue)
		})

		t.Run(fmt.Sprintf("[%v] Override in Env Var no config file", provider(config.Options{}).ID()), func(t *testing.T) {
			reg := config.NewRootSection()
			_, err := reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			v := provider(config.Options{RootSection: reg})
			key := strings.ToUpper("my-component.str3")
			assert.NoError(t, os.Setenv(key, "Set From Env"))
			defer func() { assert.NoError(t, os.Unsetenv(key)) }()
			assert.NoError(t, v.UpdateConfig(context.TODO()))
			r := reg.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			assert.Equal(t, "Set From Env", r.StringValue3)
		})

		t.Run(fmt.Sprintf("[%v] Change handler", provider(config.Options{}).ID()), func(t *testing.T) {
			configFile := tempFileName("config-*.yaml")
			defer func() { assert.NoError(t, os.Remove(configFile)) }()
			_, err := populateConfigData(configFile)
			assert.NoError(t, err)

			reg := config.NewRootSection()
			_, err = reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			opts := config.Options{
				SearchPaths: []string{configFile},
				RootSection: reg,
			}
			v := provider(opts)
			err = v.UpdateConfig(context.TODO())
			assert.NoError(t, err)

			r := reg.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			firstValue := r.StringValue

			fileUpdated, err := beginWaitForFileChange(configFile)
			assert.NoError(t, err)

			_, err = populateConfigData(configFile)
			assert.NoError(t, err)

			// Simulate filewatcher event
			assert.NoError(t, waitForFileChangeOrTimeout(fileUpdated))

			time.Sleep(2 * time.Second)

			r = reg.GetSection(MyComponentSectionKey).GetConfig().(*MyComponentConfig)
			secondValue := r.StringValue
			assert.NotEqual(t, firstValue, secondValue)
		})

		t.Run(fmt.Sprintf("[%v] Change handler k8s configmaps", provider(config.Options{}).ID()), func(t *testing.T) {
			// 1. Create Dir structure
			watchDir, configFile, cleanup := newSymlinkedConfigFile(t)
			defer cleanup()

			// Independently watch for when symlink underlying change happens to know when do we expect accessor to have picked up
			// the changes
			fileUpdated, err := beginWaitForFileChange(configFile)
			assert.NoError(t, err)

			// 2. Start accessor with the symlink as config location
			reg := config.NewRootSection()
			section, err := reg.RegisterSection(MyComponentSectionKey, &MyComponentConfig{})
			assert.NoError(t, err)

			opts := config.Options{
				SearchPaths: []string{configFile},
				RootSection: reg,
			}
			v := provider(opts)
			err = v.UpdateConfig(context.TODO())
			assert.NoError(t, err)

			r := section.GetConfig().(*MyComponentConfig)
			firstValue := r.StringValue

			// 3. Now update /data symlink to point to data2
			dataDir2 := path.Join(watchDir, "data2")
			err = os.Mkdir(dataDir2, os.ModePerm)
			assert.NoError(t, err)

			configFile2 := path.Join(dataDir2, "config.yaml")
			_, err = populateConfigData(configFile2)
			assert.NoError(t, err)

			// change the symlink using the `ln -sfn` command
			err = changeSymLink(dataDir2, path.Join(watchDir, "data"))
			assert.NoError(t, err)

			t.Logf("New config Location: %v", configFile2)

			// Wait for filewatcher event
			assert.NoError(t, waitForFileChangeOrTimeout(fileUpdated))

			time.Sleep(2 * time.Second)

			r = section.GetConfig().(*MyComponentConfig)
			secondValue := r.StringValue
			// Make sure values have changed
			assert.NotEqual(t, firstValue, secondValue)
		})
	}
}

func changeSymLink(targetPath, symLink string) error {
	if runtime.GOOS == "windows" {
		tmpLink := tempFileName("temp-sym-link-*")
		err := exec.Command("mklink", filepath.Clean(tmpLink), filepath.Clean(targetPath)).Run()
		if err != nil {
			return err
		}

		err = exec.Command("copy", "/l", "/y", filepath.Clean(tmpLink), filepath.Clean(symLink)).Run()
		if err != nil {
			return err
		}

		return exec.Command("del", filepath.Clean(tmpLink)).Run()
	}

	return exec.Command("ln", "-sfn", filepath.Clean(targetPath), filepath.Clean(symLink)).Run()
}

// 1. Create Dir structure:
//    |_ data1
//       |_ config.yaml
//    |_ data (symlink for data1)
//    |_ config.yaml (symlink for data/config.yaml -recursively a symlink of data1/config.yaml)
func newSymlinkedConfigFile(t *testing.T) (watchDir, configFile string, cleanup func()) {
	watchDir, err := ioutil.TempDir("", "config-test-")
	assert.NoError(t, err)

	dataDir1 := path.Join(watchDir, "data1")
	err = os.Mkdir(dataDir1, os.ModePerm)
	assert.NoError(t, err)

	realConfigFile := path.Join(dataDir1, "config.yaml")
	t.Logf("Real config file location: %s\n", realConfigFile)
	_, err = populateConfigData(realConfigFile)
	assert.NoError(t, err)

	cleanup = func() {
		assert.NoError(t, os.RemoveAll(watchDir))
	}

	// now, symlink the tm `data1` dir to `data` in the baseDir
	assert.NoError(t, os.Symlink(dataDir1, path.Join(watchDir, "data")))

	// and link the `<watchdir>/datadir1/config.yaml` to `<watchdir>/config.yaml`
	configFile = path.Join(watchDir, "config.yaml")
	assert.NoError(t, os.Symlink(path.Join(watchDir, "data", "config.yaml"), configFile))

	t.Logf("Config file location: %s\n", path.Join(watchDir, "config.yaml"))
	return watchDir, configFile, cleanup
}

func waitForFileChangeOrTimeout(done chan error) error {
	timeout := make(chan bool, 1)
	go func() {
		time.Sleep(5 * time.Second)
		timeout <- true
	}()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timed out")
		case err := <-done:
			return err
		}
	}
}

func beginWaitForFileChange(filename string) (done chan error, terminalErr error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	configFile := filepath.Clean(filename)
	realConfigFile, err := filepath.EvalSymlinks(configFile)
	if err != nil {
		return nil, err
	}

	configDir, _ := filepath.Split(configFile)

	done = make(chan error)
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				// we only care about the config file
				currentConfigFile, err := filepath.EvalSymlinks(filename)
				if err != nil {
					closeErr := watcher.Close()
					if closeErr != nil {
						done <- closeErr
					} else {
						done <- err
					}

					return
				}

				// We only care about the config file with the following cases:
				// 1 - if the config file was modified or created
				// 2 - if the real path to the config file changed (eg: k8s ConfigMap replacement)
				const writeOrCreateMask = fsnotify.Write | fsnotify.Create
				if (filepath.Clean(event.Name) == configFile &&
					event.Op&writeOrCreateMask != 0) ||
					(currentConfigFile != "" && currentConfigFile != realConfigFile) {
					realConfigFile = currentConfigFile
					closeErr := watcher.Close()
					if closeErr != nil {
						fmt.Printf("Close Watcher error: %v\n", closeErr)
					} else {
						done <- nil
					}

					return
				} else if filepath.Clean(event.Name) == configFile &&
					event.Op&fsnotify.Remove&fsnotify.Remove != 0 {
					closeErr := watcher.Close()
					if closeErr != nil {
						fmt.Printf("Close Watcher error: %v\n", closeErr)
					} else {
						done <- nil
					}

					return
				}
			case err, ok := <-watcher.Errors:
				if ok {
					fmt.Printf("Watcher error: %v\n", err)
					closeErr := watcher.Close()
					if closeErr != nil {
						fmt.Printf("Close Watcher error: %v\n", closeErr)
					}
				}

				done <- nil
				return
			}
		}
	}()

	err = watcher.Add(configDir)
	if err != nil {
		return nil, err
	}

	return done, err
}

func testTypes(accessor accessorCreatorFn) func(t *testing.T) {
	return func(t *testing.T) {
		t.Run("ArrayConfigType", func(t *testing.T) {
			expected := ComplexTypeArray{
				{IntValue: 1},
				{IntValue: 4},
			}

			runEqualTest(t, accessor, &expected, &ComplexTypeArray{})
		})

		t.Run("Lists", func(t *testing.T) {
			expected := ConfigWithLists{
				ListOfStuff: []ComplexType{
					{IntValue: 1},
					{IntValue: 4},
				},
			}

			runEqualTest(t, accessor, &expected, &ConfigWithLists{})
		})

		t.Run("Maps", func(t *testing.T) {
			expected := ConfigWithMaps{
				MapOfStuff: map[string]ComplexType{
					"item1": {IntValue: 1},
					"item2": {IntValue: 3},
				},
				MapWithoutJSON: map[string]ComplexType{
					"it-1": {IntValue: 5},
				},
			}

			runEqualTest(t, accessor, &expected, &ConfigWithMaps{})
		})

		t.Run("JsonUnmarshalableTypes", func(t *testing.T) {
			expected := ConfigWithJSONTypes{
				Duration: config.Duration{
					Duration: time.Second * 10,
				},
			}

			runEqualTest(t, accessor, &expected, &ConfigWithJSONTypes{})
		})
	}
}

func runEqualTest(t *testing.T, accessor accessorCreatorFn, expected interface{}, emptyType interface{}) {
	assert.NotPanics(t, func() {
		reflect.TypeOf(expected).Elem()
	}, "expected must be a Pointer type. Instead, it was %v", reflect.TypeOf(expected))

	assert.Equal(t, reflect.TypeOf(expected), reflect.TypeOf(emptyType))

	rootSection := config.NewRootSection()
	sectionKey := fmt.Sprintf("rand-key-%v", getRandInt()%2000)
	_, err := rootSection.RegisterSection(sectionKey, emptyType)
	assert.NoError(t, err)

	m := map[string]interface{}{
		sectionKey: expected,
	}

	raw, err := yaml.Marshal(m)
	assert.NoError(t, err)
	f := tempFileName("test_type_*.yaml")
	assert.NoError(t, err)
	defer func() { assert.NoError(t, os.Remove(f)) }()

	assert.NoError(t, ioutil.WriteFile(f, raw, os.ModePerm))
	t.Logf("Generated yaml: %v", string(raw))
	assert.NoError(t, accessor(rootSection, f).UpdateConfig(context.TODO()))

	res := rootSection.GetSection(sectionKey).GetConfig()
	t.Logf("Expected: %+v", expected)
	t.Logf("Actual: %+v", res)
	assert.True(t, reflect.DeepEqual(res, expected))
}

func TestAccessor_Integration(t *testing.T) {
	accessorsToTest := make([]accessorCreatorFn, 0, len(providers))
	for _, provider := range providers {
		accessorsToTest = append(accessorsToTest, func(r config.Section, configPath string) config.Accessor {
			return provider(config.Options{
				SearchPaths: []string{configPath},
				RootSection: r,
			})
		})
	}

	for _, accessor := range accessorsToTest {
		t.Run(fmt.Sprintf(testNameFormatter, accessor(nil, "").ID(), "Types"), testTypes(accessor))
	}
}
