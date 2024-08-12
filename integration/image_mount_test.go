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
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageMount(t *testing.T) {
	var (
		testImage      = images.Get(images.BusyBox)
		testMountImage = images.Get(images.Pause)
		containerName  = "test-container"
	)
	EnsureImageExists(t, testMountImage)
	EnsureImageExists(t, testImage)

	testPodLogDir := t.TempDir()

	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox",
		"image-mount",
		WithHostNetwork,
		WithPodLogDirectory(testPodLogDir),
	)
	containerConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("ls", "/image-mount"),
		WithImageVolumeMount(testMountImage, "/image-mount"),
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
		if s.GetState() == runtime.ContainerState_CONTAINER_EXITED {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)
	checkContainerLog(t, string(content), []string{
		fmt.Sprintf("%s %s %s", runtime.Stdout, runtime.LogTagFull, "pause"),
	})
}
