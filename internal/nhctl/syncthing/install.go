/*
Copyright 2020 The Nocalhost Authors.
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

package syncthing

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	getter "github.com/hashicorp/go-getter"

	"nocalhost/pkg/nhctl/log"
	"nocalhost/pkg/nhctl/utils"
)

const SyncthingVersion = "latest"

var (
	downloadURLs = map[string]string{
		"linux":   "https://codingcorp-generic.pkg.coding.net/nocalhost/syncthing/syncthing-linux-amd64.tar.gz?version=%s",
		"arm64":   "https://codingcorp-generic.pkg.coding.net/nocalhost/syncthing/syncthing-linux-arm64.tar.gz?version=%s",
		"darwin":  "https://codingcorp-generic.pkg.coding.net/nocalhost/syncthing/syncthing-macos-amd64.zip?version=%s",
		"windows": "https://codingcorp-generic.pkg.coding.net/nocalhost/syncthing/syncthing-windows-amd64.zip?version=%s",
	}
)

func (s *Syncthing) DownloadSyncthing(version string) error {
	t := time.NewTicker(1 * time.Second)
	var err error
	for i := 0; i < 3; i++ {
		p := &utils.ProgressBar{}
		err = s.Install(version, p)
		if err == nil {
			return nil
		}

		if i < 2 {
			log.Fatalf("failed to download syncthing, retrying: %s", err)
			<-t.C
		}
	}

	return err
}

// Install installs syncthing
func (s *Syncthing) Install(version string, p getter.ProgressTracker) error {
	fmt.Printf("installing syncthing for %s/%s", runtime.GOOS, runtime.GOARCH)
	downloadURL, err := GetDownloadURL(runtime.GOOS, runtime.GOARCH, version)
	if err != nil {
		return err
	}

	opts := []getter.ClientOption{}
	if p != nil {
		opts = []getter.ClientOption{getter.WithProgress(p)}
	}

	dir, err := ioutil.TempDir("", "")
	if err != nil {
		return fmt.Errorf("failed to create temp download dir")
	}

	client := &getter.Client{
		Src:     downloadURL,
		Dst:     dir,
		Mode:    getter.ClientModeDir,
		Options: opts,
	}

	defer os.RemoveAll(dir)

	if err := client.Get(); err != nil {
		return fmt.Errorf("failed to download syncthing from %s: %s", client.Src, err)
	}

	dirs, err := ioutil.ReadDir(client.Dst)
	if err != nil {
		return err
	}
	if len(dirs) != 1 {
		return fmt.Errorf("download syncthing from %s and extract multi dirs that are not expected", client.Src)
	}

	info := dirs[0]
	if !info.IsDir() {
		return fmt.Errorf("download syncthing from %s and there is an unpredictable error occurred during decompression", client.Src)
	}

	i := s.BinPath
	b := getBinaryPathInDownload(dir, info.Name())

	if _, err := os.Stat(b); err != nil {
		return fmt.Errorf("%s didn't include the syncthing binary: %s", downloadURL, err)
	}

	if err := os.Chmod(b, 0700); err != nil {
		return fmt.Errorf("failed to set permissions to %s: %s", b, err)
	}

	if FileExists(i) {
		if err := os.Remove(i); err != nil {
			log.Debugf("failed to delete %s, will try to overwrite: %s", i, err)
		}
	}

	// fix if ~/.nh/nhctl/bin/syncthing not exist
	if err := os.MkdirAll((GetDir(i)), 0700); err != nil {
		return fmt.Errorf("failed mkdir for %s: %s", i, err)
	}

	if err := CopyFile(b, i); err != nil {
		return fmt.Errorf("failed to write %s: %s", i, err)
	}

	fmt.Printf("downloaded syncthing %s to %s\n", version, i)
	return nil
}

func CopyFile(from, to string) error {
	fromFile, err := os.Open(from)
	if err != nil {
		return err
	}
	toFile, err := os.OpenFile(to, os.O_RDWR|os.O_CREATE, 0700)
	if err != nil {
		return err
	}

	defer toFile.Close()

	_, err = io.Copy(toFile, fromFile)
	if err != nil {
		return err
	}

	return nil
}

func GetDir(path string) string {
	return path[:strings.LastIndex(path, string(os.PathSeparator))]
}

func FileExists(name string) bool {
	_, err := os.Stat(name)
	if os.IsNotExist(err) {
		return false
	}

	if err != nil {
		log.Debugf("Failed to check if %s exists: %s", name, err)
	}

	return true
}

func (s *Syncthing) IsInstalled() bool {
	_, err := os.Stat(s.BinPath)
	return !os.IsNotExist(err)
}

func (s *Syncthing) NeedToDownloadSpecifyVersion(nhctlVersion string) bool {
	cmdArgs := []string{
		"-nocalhost",
	}

	output, err := exec.Command(s.BinPath, cmdArgs...).Output()

	if err != nil {
		log.Infof("Need to download due to syncthing exec fail, error: %s", err)
		return true
	}

	currentSyncthingVersion := strings.TrimSuffix(string(output), "\n")
	log.Infof("current syncthing version: %s \ncurrent nhctl version: %s", currentSyncthingVersion, nhctlVersion)

	download := currentSyncthingVersion != nhctlVersion
	if download {
		log.Infof("need to download syncthing with nocalhost version: " + nhctlVersion)
	}

	return download
}

func GetDownloadURL(os, arch, version string) (string, error) {
	src, ok := downloadURLs[os]
	if !ok {
		return "", fmt.Errorf("%s is not a supported platform", os)
	}

	if os == "linux" {
		switch arch {
		case "arm":
			return downloadURLs["arm"], nil
		case "arm64":
			return downloadURLs["arm64"], nil
		}
	}

	return fmt.Sprintf(src, version), nil
}

func getBinaryPathInDownload(dir, subdir string) string {
	return filepath.Join(dir, subdir, GetBinaryName())
}

func GetBinaryName() string {
	if runtime.GOOS == "windows" {
		return "syncthing.exe"
	}
	return "syncthing"
}
