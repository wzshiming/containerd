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
	"strings"
	"syscall"
	"unsafe"
)

var longPathName = syscall.NewLazyDLL("kernel32.dll").NewProc("GetLongPathNameW")

func getLongPathName(shortPath string) (string, error) {
	if !strings.Contains(shortPath, "~") {
		return shortPath, nil
	}
	lpPath, err := syscall.UTF16FromString(shortPath)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, syscall.MAX_PATH)
	ret, _, err := longPathName.Call(
		uintptr(unsafe.Pointer(&lpPath[0])),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if ret == 0 {
		return "", err
	}
	return syscall.UTF16ToString(buf), nil
}
