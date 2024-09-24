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

package server

import (
	"context"
	"fmt"
	"os"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	labelGCSnapRef = "containerd.io/gc.ref.snapshot."
)

func (c *criService) mutateMounts(
	ctx context.Context,
	extraMounts []*runtime.Mount,
	snapshotter string,
	sandboxID string,
	platform imagespec.Platform,

) error {
	for _, m := range extraMounts {
		err := c.mutateImageMount(ctx, m, snapshotter, sandboxID, platform)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *criService) mutateImageMount(
	ctx context.Context,
	extraMount *runtime.Mount,
	snapshotter string,
	sandboxID string,
	platform imagespec.Platform,
) (retErr error) {
	imageSpec := extraMount.GetImage()
	if imageSpec == nil {
		return nil
	}
	if extraMount.GetHostPath() != "" {
		return fmt.Errorf("hostpath must be empty while mount image: %+v", extraMount)
	}
	if !extraMount.GetReadonly() {
		return fmt.Errorf("readonly must be true while mount image: %+v", extraMount)
	}

	ref := imageSpec.GetImage()
	if ref == "" {
		return fmt.Errorf("image not specified in: %+v", imageSpec)
	}
	image, err := c.LocalResolve(ref)
	if err != nil {
		return fmt.Errorf("failed to resolve image %q: %w", ref, err)
	}
	containerdImage, err := c.toContainerdImage(ctx, image)
	if err != nil {
		return fmt.Errorf("failed to get image from containerd %q: %w", image.ID, err)
	}

	// This is a digest of the manifest
	imageID := containerdImage.Target().Digest.Hex()

	target := c.getImageVolumeHostPath(sandboxID, imageID)

	// Already mounted in another container on the same pod
	if stat, err := os.Stat(target); err == nil && stat.IsDir() {
		extraMount.HostPath = target
		return nil
	}

	ctx, done, err := c.client.WithLease(ctx,
		leases.WithID(target),
		leases.WithLabel(defaults.DefaultSnapshotterNSLabel, snapshotter),
		leases.WithLabel(labelGCSnapRef+snapshotter, target),
	)
	if err != nil && !errdefs.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create lease: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = done(ctx) // call lease done func.
		}
	}()

	img, err := c.client.ImageService().Get(ctx, ref)
	if err != nil {
		return fmt.Errorf("failed to get image volume ref %q: %w", ref, err)
	}

	i := containerd.NewImageWithPlatform(c.client, img, platforms.Only(platform))
	if err := i.Unpack(ctx, snapshotter); err != nil {
		return fmt.Errorf("failed to unpack image volume: %w", err)
	}

	diffIDs, err := i.RootFS(ctx)
	if err != nil {
		return fmt.Errorf("failed to get diff IDs for image volume %q: %w", ref, err)
	}
	chainID := identity.ChainID(diffIDs).String()

	s := c.client.SnapshotService(snapshotter)
	mounts, err := s.Prepare(ctx, target, chainID)
	if err != nil {
		return fmt.Errorf("failed to prepare for image volume %q: %w", ref, err)
	}
	defer func() {
		if retErr != nil {
			_ = s.Remove(ctx, target)
		}
	}()

	err = os.MkdirAll(target, 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory to image volume target path %q: %w", target, err)
	}

	if err := mount.All(mounts, target); err != nil {
		return fmt.Errorf("failed to mount image volume component %q: %w", target, err)
	}

	extraMount.HostPath = target
	return nil
}

func (c *criService) mutateUnmounts(
	ctx context.Context,
	extraMounts []*runtime.Mount,
	snapshotter string,
) error {
	for _, m := range extraMounts {
		err := c.mutateImageUnmount(ctx, m, snapshotter)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *criService) mutateImageUnmount(
	ctx context.Context,
	extraMount *runtime.Mount,
	snapshotter string,
) (retErr error) {
	if extraMount.Image == nil {
		return nil
	}

	target := extraMount.HostPath
	if target == "" {
		return nil
	}

	// Already unmounted from another container on the same pod
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return nil
	}

	err := mount.UnmountAll(target, 0)
	if err != nil {
		return fmt.Errorf("failed to unmount image volume component %q: %w", target, err)
	}
	err = c.client.LeasesService().Delete(ctx, leases.Lease{ID: target})
	if err != nil {
		return fmt.Errorf("failed to deleting lease: %w", err)
	}

	s := c.client.SnapshotService(snapshotter)
	err = s.Remove(ctx, target)
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to removing snapshot: %w", err)
	}

	return nil
}