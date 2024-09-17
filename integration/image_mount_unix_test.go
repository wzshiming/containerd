//go:build !windows

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageMount(t *testing.T) {
	testImage := images.Get(images.Alpine)
	testMountImage := images.Get(images.Pause)
	mountPath := "/image-mount"
	EnsureImageExists(t, testMountImage)
	EnsureImageExists(t, testImage)
	testImageMount(t, testImage, testMountImage, mountPath, []string{
		"ls",
		mountPath,
	}, []string{
		fmt.Sprintf("%s %s %s", criruntime.Stdout, criruntime.LogTagFull, "pause"),
	})
}

func TestImageMountShared(t *testing.T) {
	testImage := images.Get(images.Alpine)
	testMountImage := images.Get(images.Pause)
	mountPath := "/image-mount"
	EnsureImageExists(t, testMountImage)
	EnsureImageExists(t, testImage)
	wg := sync.WaitGroup{}
	for i := 0; i != 5; i++ {
		wg.Add(1)
		time.Sleep(time.Duration(i) * time.Second)
		go func(i int) {
			defer wg.Done()
			testImageMount(t, testImage, testMountImage, mountPath, []string{
				"sh",
				"-c",
				fmt.Sprintf("ls %s; sleep %d; echo %d; ls %s", mountPath, i*i, i, mountPath),
			}, []string{
				fmt.Sprintf("%s %s %s", criruntime.Stdout, criruntime.LogTagFull, "pause"),
				fmt.Sprintf("%s %s %d", criruntime.Stdout, criruntime.LogTagFull, i),
				fmt.Sprintf("%s %s %s", criruntime.Stdout, criruntime.LogTagFull, "pause"),
			})
		}(i)
	}

	wg.Wait()
}

func TestImageMountSELinux(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("Only running on linux")
	}

	if !selinux.GetEnabled() {
		t.Skip("SELinux is not enabled")
	}

	testImage := images.Get(images.ResourceConsumer)
	testMountImage := images.Get(images.Pause)
	mountPath := "/image-mount"
	EnsureImageExists(t, testMountImage)
	EnsureImageExists(t, testImage)
	testImageMountSELinux(t, testImage, testMountImage, mountPath, "s0:c4,c5", "system_u:object_r:container_file_t:s0:c4,c5 pause")
	testImageMountSELinux(t, testImage, testMountImage, mountPath, "s0:c200,c100", "system_u:object_r:container_file_t:s0:c100,c200 pause")
}

func testImageMountSELinux(t *testing.T, testImage, testMountImage, mountPath string, level string, want string) {
	var (
		containerName = "test-image-mount-container"
	)

	testPodLogDir := t.TempDir()

	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox",
		"image-mount",
		WithHostNetwork,
		WithSelinuxLevel(level),
		WithPodLogDirectory(testPodLogDir),
	)

	containerConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("ls", "-Z", mountPath),
		WithImageVolumeMount(testMountImage, mountPath),
		WithLogPath(containerName),
	)

	cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	require.NoError(t, runtimeService.StartContainer(cn))

	require.NoError(t, Eventually(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		if s.GetState() == criruntime.ContainerState_CONTAINER_EXITED {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)
	checkContainerLog(t, string(content), []string{
		fmt.Sprintf("%s %s %s", criruntime.Stdout, criruntime.LogTagFull, want),
	})
}
